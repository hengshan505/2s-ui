package core

import (
	"encoding/json"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
)

// userKicker is what the protocol copies in core/protocol/ expose in their
// users.go: a way to reach the session registry from outside the inbound.
// What it counts as Unclosable is only believed for some inbound types; see
// unclosableReason.
type userKicker interface {
	KickUser(user string) usersession.Result
}

// Why a kick left an inbound with a session it could not cut. These are what
// the panel turns into words, so they are part of the closeSessions answer.
const (
	// UnclosableQUIC is a Hysteria, Hysteria2 or TUIC inbound: this layer has no
	// handle on a QUIC session (see core/usersession).
	UnclosableQUIC = "quic"
	// UnclosableMux is a Shadowsocks inbound with multiplex on. Shadowsocks has
	// no copy in core/protocol/, so its carrier is out of reach; only the service
	// layer can tell such an inbound apart, and it adds these.
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

// DisconnectResult is what disconnecting one user managed to do. Its JSON is
// DisconnectWire's.
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
	// connected by a session this layer cannot reach. Their routed connections
	// are closed, but the client opens new streams on the session it still has.
	UnclosableInbounds []UnclosableInbound `json:"unclosableInbounds,omitempty"`
}

// DisconnectWire is DisconnectResult as it is sent.
type DisconnectWire struct {
	Connections int `json:"connections"`
	Sessions    int `json:"sessions"`
	// Unclosable is len(UnclosableInbounds), for masters that predate the list:
	// they read only this count, and warn when it is not zero.
	Unclosable         int                 `json:"unclosable"`
	UnclosableInbounds []UnclosableInbound `json:"unclosableInbounds,omitempty"`
}

// Wire is r with its count filled in. A type that embeds DisconnectResult and
// adds fields of its own needs it for a MarshalJSON of its own: the one below
// would otherwise be promoted and marshal the embedded half alone.
func (r DisconnectResult) Wire() DisconnectWire {
	return DisconnectWire{
		Connections:        r.Connections,
		Sessions:           r.Sessions,
		Unclosable:         len(r.UnclosableInbounds),
		UnclosableInbounds: r.UnclosableInbounds,
	}
}

func (r DisconnectResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.Wire())
}

// AddUnclosable records one inbound left with a session that could not be cut.
func (r *DisconnectResult) AddUnclosable(tag string, reason string) {
	r.UnclosableInbounds = append(r.UnclosableInbounds, UnclosableInbound{Tag: tag, Reason: reason})
}

// unclosableReason says why an inbound of this type can keep a session that a
// kick cannot cut, or "" when it cannot.
//
// Only the QUIC inbounds can. anytls and the mux protocols hold the transport
// of every session they could have, so whatever their registry counts on a kick
// is a stray stream that reached the router after its session had ended -- one
// that landed in the seen set, which a kick never clears. Deciding by type here,
// rather than trusting each copy to leave such strays out of its KickUser,
// is what keeps one from being reported as a QUIC session on every later kick.
func unclosableReason(inboundType string) string {
	switch inboundType {
	case C.TypeHysteria, C.TypeHysteria2, C.TypeTUIC:
		return UnclosableQUIC
	}
	return ""
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
//
// live maps each inbound the user had routed connections on to its type, for
// the caller to look for what only it can recognise among them.
func (c *Core) DisconnectUser(user string) (result DisconnectResult, live map[string]string, err error) {
	box, err := c.running()
	if err != nil {
		return result, nil, err
	}
	result, live = disconnect(box.ConnTracker(), box.Inbound().Inbounds(), user)
	return result, live, nil
}

// disconnect is DisconnectUser on a given tracker and inbound list.
//
// An inbound is reported only when the user had a routed connection on it a
// moment ago. For QUIC the registry knows only that the user has been seen on
// the inbound, ever, which is right for a removal and wrong as a report on a
// kick: one connection to a QUIC inbound used to make every later kick warn
// about it for as long as the inbound lived, and the warning tells the
// operator to disable the client (#234). The price is the opposite miss -- a
// QUIC session with no stream open at this instant goes unreported. For a kick
// that is the cheap side: the client may reconnect at once anyway, so the
// session it keeps buys it nothing it could not get back.
func disconnect(conns userConns, inbounds []adapter.Inbound, user string) (DisconnectResult, map[string]string) {
	var result DisconnectResult
	// Routed connections first, in the one pass that also records which
	// inbounds they were on. That record is the only sign of where the user is
	// connected, so nothing may close a connection before it is taken -- and a
	// cut carrier takes its streams with it. Closing streams ahead of their
	// carrier costs nothing: the carrier goes a moment later, and whatever the
	// client opened on it in between goes with it.
	closed, on := conns.CloseConnByUser(user)
	result.Connections = closed

	live := make(map[string]string, len(on))
	for _, inbound := range inbounds {
		tag := inbound.Tag()
		_, wasOn := on[tag]
		if wasOn {
			live[tag] = inbound.Type()
		}
		kicker, ok := inbound.(userKicker)
		if !ok {
			continue
		}
		r := kicker.KickUser(user)
		result.Sessions += r.Cut
		if reason := unclosableReason(inbound.Type()); r.Unclosable > 0 && reason != "" && wasOn {
			result.AddUnclosable(tag, reason)
		}
	}
	return result, live
}
