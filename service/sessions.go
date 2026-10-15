package service

import (
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/shenaba/2s-ui/core"
	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"
	"github.com/shenaba/2s-ui/util/common"
)

// SessionsResult is a live-connections listing. Errors names the nodes that
// could not be asked, so a partial list is never mistaken for a complete one.
type SessionsResult struct {
	Sessions []core.SessionInfo `json:"sessions"`
	Errors   map[string]string  `json:"errors,omitempty"`
}

// LocalSessions lists this panel's own live routed connections, narrowed to
// one user, inbound or outbound (an empty tag means all of them). Newest first,
// so a connection that was just opened is at the top.
//
// resource is checked whether or not a tag came with it: a misspelt one with no
// tag would otherwise read as "everything" and hand out every user's list.
func LocalSessions(resource string, tag string) ([]core.SessionInfo, error) {
	var field func(info *core.ConnectionInfo) string
	switch resource {
	case "user":
		field = func(info *core.ConnectionInfo) string { return info.User }
	case "inbound":
		field = func(info *core.ConnectionInfo) string { return info.Inbound }
	case "outbound":
		field = func(info *core.ConnectionInfo) string { return info.Outbound }
	case "":
		// Everything, which only makes sense without a tag to narrow it by.
		if tag != "" {
			return nil, common.NewError("tag needs a resource")
		}
	default:
		return nil, common.NewError("unknown resource: ", resource)
	}
	var want func(info *core.ConnectionInfo) bool
	if tag != "" {
		want = func(info *core.ConnectionInfo) bool { return field(info) == tag }
	}
	tracker := liveConnTracker()
	if tracker == nil {
		return []core.SessionInfo{}, nil
	}
	sessions := tracker.Sessions(want)
	sortSessions(sessions)
	return sessions, nil
}

func sortSessions(sessions []core.SessionInfo) {
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt > sessions[j].CreatedAt
	})
}

// ClusterUserSessions is LocalSessions for one client, plus the connections it
// holds on any node it is currently online on.
//
// Only a user is merged, never an inbound or outbound: those tags name objects
// on this panel, and a node's inbound of the same tag is a different listener.
//
// The nodes asked are the ones whose last probe listed the client as online.
// That is what made the panel show it online in the first place, so a node it
// is not on costs no request -- and a client online on no node costs none.
func ClusterUserSessions(user string) (SessionsResult, error) {
	local, err := LocalSessions("user", user)
	if err != nil {
		return SessionsResult{}, err
	}
	result := SessionsResult{Sessions: local}
	if user == "" {
		return result, nil
	}

	nodes, err := nodesWithOnlineUser(user)
	if err != nil {
		return SessionsResult{}, err
	}
	if len(nodes) == 0 {
		return result, nil
	}

	var ns NodeService
	q := url.Values{"resource": {"user"}, "tag": {user}}
	result.Errors = eachNode(nodes,
		func(n *model.Node) ([]core.SessionInfo, error) { return fetchNodeSessions(&ns, n, q) },
		func(n *model.Node, sessions []core.SessionInfo) {
			for i := range sessions {
				sessions[i].Node = n.Name
			}
			result.Sessions = append(result.Sessions, sessions...)
		})
	sortSessions(result.Sessions)
	return result, nil
}

// eachNode runs call against every node, at most nodeProbeParallel at a time,
// and hands each answer to merge. merge runs under a lock, so it may write to
// shared state without one of its own. The result names the nodes that failed,
// and is nil when none did -- what the omitempty Errors fields want.
func eachNode[T any](nodes []*model.Node, call func(n *model.Node) (T, error), merge func(n *model.Node, v T)) map[string]string {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs map[string]string
	)
	sem := make(chan struct{}, nodeProbeParallel)
	for _, n := range nodes {
		wg.Add(1)
		sem <- struct{}{}
		go func(n *model.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := call(n)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errs == nil {
					errs = map[string]string{}
				}
				errs[n.Name] = err.Error()
				return
			}
			merge(n, v)
		}(n)
	}
	wg.Wait()
	return errs
}

// fetchNodeSessions asks one node for its list. The pooled client already
// carries nodeProbeTimeout, so a node slow to answer costs its own rows, not
// the whole modal.
func fetchNodeSessions(ns *NodeService, n *model.Node, q url.Values) ([]core.SessionInfo, error) {
	raw, err := ns.nodeGet(n, nodeHTTPClient(n), "sessions", q)
	if err != nil {
		return nil, err
	}
	var listed SessionsResult
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, common.NewError("unexpected response from node")
	}
	return listed.Sessions, nil
}

// nodesWithOnlineUser returns the enabled nodes whose fresh probe lists user.
func nodesWithOnlineUser(user string) ([]*model.Node, error) {
	now := time.Now().Unix()
	var ids []uint
	nodeStatusMu.RLock()
	for id, status := range nodeStatuses {
		if status.State != "online" || status.onlineCheckedAt == 0 ||
			now-status.onlineCheckedAt > int64(nodeOnlineTTL.Seconds()) {
			continue
		}
		for _, name := range status.onlineUsers {
			if name == user {
				ids = append(ids, id)
				break
			}
		}
	}
	nodeStatusMu.RUnlock()
	return enabledNodes(ids)
}

// onlineNodes returns the enabled nodes whose last probe found their core
// running, whether or not they list any particular client.
func onlineNodes() ([]*model.Node, error) {
	var ids []uint
	nodeStatusMu.RLock()
	for id, status := range nodeStatuses {
		if status.State == "online" {
			ids = append(ids, id)
		}
	}
	nodeStatusMu.RUnlock()
	return enabledNodes(ids)
}

func enabledNodes(ids []uint) ([]*model.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []*model.Node
	err := database.GetDB().Model(model.Node{}).Where("id IN ? AND enable = ?", ids, true).Find(&nodes).Error
	return nodes, err
}

// DisconnectResult is the merged answer, plus what went wrong: the nodes that
// could not be told, and this panel's own failure when nodes were asked too.
//
// It embeds core.DisconnectWire rather than core.DisconnectResult: that one's
// MarshalJSON would be promoted and marshal it alone. Embedding the wire form
// sends every field of both with no marshaller here to keep in step.
type DisconnectResult struct {
	core.DisconnectWire
	Errors     map[string]string `json:"errors,omitempty"`
	LocalError string            `json:"localError,omitempty"`
}

// DisconnectLocalUser drops one user's live connections on this panel only.
func DisconnectLocalUser(user string) (core.DisconnectResult, error) {
	if user == "" {
		return core.DisconnectResult{}, common.NewError("empty user name")
	}
	if corePtr == nil {
		return core.DisconnectResult{}, common.NewError("sing-box is not running")
	}
	return corePtr.DisconnectUser(user)
}

// nodeUnclosable labels the inbounds a node could not cut with the node's name.
//
// A node from before per-inbound reports sends a count and no list, and that
// count is dropped -- core.DisconnectResult has no field to decode it into. It
// comes from the registry's seen set alone, the signal #234 found too weak for
// a kick, so a warning built from it cannot be told from a false one, and a
// false one asks the operator to disable a client for nothing. Missing a real
// one costs little: a kick lets the client straight back in anyway. Such a
// node warns again once it is upgraded.
func nodeUnclosable(node string, remote core.DisconnectResult) []core.UnclosableInbound {
	labelled := make([]core.UnclosableInbound, 0, len(remote.UnclosableInbounds))
	for _, u := range remote.UnclosableInbounds {
		u.Node = node
		labelled = append(labelled, u)
	}
	return labelled
}

// DisconnectClusterUser drops a user's live connections here and on every
// online node. The user stays enabled everywhere, so it can reconnect at once
// -- this is a kick, not a ban.
//
// Every online node is told, not just the ones listing the client: a node's
// online list only holds clients that moved traffic in its last 10s flush, so
// an idle connection there would survive the kick. A node the client is not on
// answers with zero.
//
// A core that is not running here is not an error when nodes are involved:
// the client can perfectly well be online on a node only. A local failure is
// returned in LocalError then, next to what the nodes did; with no node to
// ask, it is the error.
func DisconnectClusterUser(user string) (DisconnectResult, error) {
	if user == "" {
		return DisconnectResult{}, common.NewError("empty user name")
	}
	var merged core.DisconnectResult
	var localErr error
	localRunning := corePtr != nil && corePtr.IsRunning()
	if localRunning {
		merged, localErr = corePtr.DisconnectUser(user)
	}

	nodes, err := onlineNodes()
	if err != nil {
		return DisconnectResult{DisconnectWire: merged.Wire()}, err
	}
	if len(nodes) == 0 {
		if !localRunning {
			return DisconnectResult{}, common.NewError("sing-box is not running")
		}
		return DisconnectResult{DisconnectWire: merged.Wire()}, localErr
	}
	var result DisconnectResult
	if localErr != nil {
		result.LocalError = localErr.Error()
	}

	var ss NodeSyncService
	form := url.Values{"u": {user}}
	result.Errors = eachNode(nodes,
		func(n *model.Node) (core.DisconnectResult, error) {
			var remote core.DisconnectResult
			raw, err := ss.nodePost(n, nodeHTTPClient(n), "closeSessions", form)
			if err == nil {
				err = json.Unmarshal(raw, &remote)
			}
			return remote, err
		},
		func(n *model.Node, remote core.DisconnectResult) {
			merged.Connections += remote.Connections
			merged.Sessions += remote.Sessions
			merged.UnclosableInbounds = append(merged.UnclosableInbounds, nodeUnclosable(n.Name, remote)...)
		})
	// The same order the core's own list is in, so that it does not depend on
	// which node answered first, nor on whether any node did.
	slices.SortStableFunc(merged.UnclosableInbounds, core.CompareUnclosable)
	result.DisconnectWire = merged.Wire()
	return result, nil
}
