package core

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// userKicker is what the protocol copies in core/protocol/ expose in their
// users.go: a way to reach the session registry from outside the inbound.
// What it counts as Unclosable is only believed for the QUIC types; see
// quicInbound.
type userKicker interface {
	KickUser(user string) usersession.Result
}

// Why a kick left an inbound with a session it could not cut. These are what
// the panel turns into words, so they are part of the closeSessions answer.
const (
	// UnclosableQUIC is a Hysteria, Hysteria2 or TUIC inbound: this layer has no
	// handle on a QUIC session (see core/usersession).
	UnclosableQUIC = "quic"
	// UnclosableMux is a Shadowsocks inbound running with multiplex on.
	// Shadowsocks has no copy in core/protocol/, so its carrier is out of reach.
	UnclosableMux = "mux"
)

// UnclosableInbound is one inbound where a kicked user may still hold a session.
type UnclosableInbound struct {
	Tag    string `json:"tag"`
	Reason string `json:"reason"`
	// Node is set by a master merging the answer a node returned; this package
	// never fills it.
	Node string `json:"node,omitempty"`
}

// CompareUnclosable is the order of a report: this panel's own inbounds first,
// then each node's, by tag within each. It is the one rule for both this
// package's list and a master's merged one, so a list reads the same however
// it was put together.
func CompareUnclosable(a, b UnclosableInbound) int {
	return cmp.Or(cmp.Compare(a.Node, b.Node), cmp.Compare(a.Tag, b.Tag))
}

// DisconnectResult is what disconnecting one user managed to do. It is sent as
// DisconnectWire.
//
// There is deliberately no count field. Masters from before the list read only
// "unclosable", so the wire form still carries one -- derived from the list,
// so the two cannot disagree. Reading an answer back has nowhere to put it,
// which is how a count-only answer from such a node is dropped (see
// service.nodeUnclosable).
type DisconnectResult struct {
	// Connections counts the routed connections closed.
	Connections int `json:"connections"`
	// Sessions counts the multiplex / anytls sessions cut outright.
	Sessions int `json:"sessions"`
	// UnclosableInbounds names the inbounds where the user is likely still
	// connected by a session this layer cannot reach, in CompareUnclosable
	// order. Their routed connections are closed, but the client opens new
	// streams on the session it still has.
	UnclosableInbounds []UnclosableInbound `json:"unclosableInbounds,omitempty"`
}

// disconnectFields is DisconnectResult without its methods, so that embedding
// it carries every field along and not the MarshalJSON below.
type disconnectFields DisconnectResult

// DisconnectWire is DisconnectResult as it is sent. Its JSON is
//
//	connections, sessions, unclosableInbounds  -- every field of DisconnectResult
//	unclosable                                 -- the count derived from the list
//
// The first three come from the embedded copy, so a field added to
// DisconnectResult is sent with nothing to change here. They are fields of a
// DisconnectWire too (w.Connections), though godoc lists only the count.
type DisconnectWire struct {
	disconnectFields
	// Unclosable is len(UnclosableInbounds), for masters that predate the list:
	// they read only this count, and warn when it is not zero.
	Unclosable int `json:"unclosable"`
}

// Wire is r as it is sent. A type that carries a DisconnectResult next to
// fields of its own embeds this rather than r: r's MarshalJSON would be
// promoted and marshal r alone.
func (r DisconnectResult) Wire() DisconnectWire {
	return DisconnectWire{disconnectFields: disconnectFields(r), Unclosable: len(r.UnclosableInbounds)}
}

func (r DisconnectResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Wire())
}

// AddUnclosable records one inbound left with a session that could not be cut.
func (r *DisconnectResult) AddUnclosable(tag string, reason string) {
	r.UnclosableInbounds = append(r.UnclosableInbounds, UnclosableInbound{Tag: tag, Reason: reason})
}

// quicInbound says whether an inbound of this type can keep a session that a
// kick cannot cut because it is QUIC.
//
// Only these can, of the inbounds with a registry. anytls and the mux
// protocols hold the transport of every session they could have, so whatever
// their registry counts on a kick is a stray stream that reached the router
// after its session had ended -- one that landed in the seen set, which a kick
// never clears. Deciding by type here, rather than trusting each copy to leave
// such strays out of its KickUser, is what keeps one from being reported as a
// QUIC session on every later kick.
func quicInbound(inboundType string) bool {
	switch inboundType {
	case C.TypeHysteria, C.TypeHysteria2, C.TypeTUIC:
		return true
	}
	return false
}

// shadowsocksMultiplex says whether options, the ones a Shadowsocks inbound
// was created with, turn multiplex on. That a client may use multiplex is not
// that it does, so a report built on it can over-warn.
func shadowsocksMultiplex(options any) bool {
	switch o := options.(type) {
	case *option.ShadowsocksInboundOptions:
		return o != nil && o.Multiplex != nil && o.Multiplex.Enabled
	case option.ShadowsocksInboundOptions:
		return o.Multiplex != nil && o.Multiplex.Enabled
	}
	return false
}

// userConns is the part of ConnTracker a kick uses.
type userConns interface {
	CloseConnByUser(user string) (closed int, inbounds map[string]struct{})
}

// DisconnectUser drops every live connection of a user who stays enabled: an
// operator action, so nothing stops them from connecting again.
//
// It deliberately never rebuilds an inbound. That is the only way to end a
// QUIC session here (see core/usersession), and it would disconnect everyone
// else on the inbound too -- a price a removal pays, but not a button that is
// meant for looking at one client. UnclosableInbounds says where that limit
// was hit.
func (c *Core) DisconnectUser(user string) (DisconnectResult, error) {
	box, err := c.running()
	if err != nil {
		return DisconnectResult{}, err
	}
	return disconnect(box.ConnTracker(), box.Inbound().Inbounds(), user, box.inbound.options), nil
}

// disconnect is DisconnectUser on a given tracker and inbound list. optionsOf
// returns what an inbound was created with.
//
// An inbound is reported only when the user had a routed connection on it a
// moment ago. For QUIC the registry knows only that the user has been seen on
// the inbound, ever, which is right for a removal and wrong as a report on a
// kick: one connection to a QUIC inbound used to make every later kick warn
// about it for as long as the inbound lived, and the warning tells the
// operator to disable the client (#234). The price is the opposite miss -- a
// session with no stream open at this instant goes unreported. For a kick
// that is the cheap side: the client may reconnect at once anyway, so the
// session it keeps buys it nothing it could not get back.
//
// Both reasons are judged on the running core -- the inbound's type and the
// options it was built from -- not on the database, which may already hold an
// edit whose restart is still pending.
func disconnect(conns userConns, inbounds []adapter.Inbound, user string, optionsOf func(tag string) any) DisconnectResult {
	var result DisconnectResult
	// Routed connections first, in the one pass that also records which
	// inbounds they were on. That record is the only sign of where the user is
	// connected, so nothing may close a connection before it is taken -- and a
	// cut carrier takes its streams with it. Closing streams ahead of their
	// carrier costs nothing: the carrier goes a moment later, and whatever the
	// client opened on it in between goes with it.
	closed, on := conns.CloseConnByUser(user)
	result.Connections = closed

	for _, inbound := range inbounds {
		tag := inbound.Tag()
		_, wasOn := on[tag]
		if kicker, ok := inbound.(userKicker); ok {
			r := kicker.KickUser(user)
			result.Sessions += r.Cut
			if r.Unclosable > 0 && wasOn && quicInbound(inbound.Type()) {
				result.AddUnclosable(tag, UnclosableQUIC)
			}
			continue
		}
		if wasOn && inbound.Type() == C.TypeShadowsocks && shadowsocksMultiplex(optionsOf(tag)) {
			result.AddUnclosable(tag, UnclosableMux)
		}
	}
	slices.SortFunc(result.UnclosableInbounds, CompareUnclosable)
	return result
}
