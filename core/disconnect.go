package core

import (
	"sort"

	"github.com/shenaba/2s-ui/core/usersession"

	"github.com/sagernet/sing-box/adapter"
)

// userKicker is what the protocol copies in core/protocol/ expose in their
// users.go: a way to reach the session registry from outside the inbound.
//
// Only the QUIC copies report Unclosable from it. The others hold the transport
// of every session they could have, so anything they would count is a stray
// stream rather than a session (see their KickUser).
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
	// UnclosableUnreported is never produced here: it is what a master records
	// for a node from before per-inbound reports, which sends only a count.
	UnclosableUnreported = "unreported"
)

// UnclosableInbound is one inbound where a kicked user may still hold a session.
type UnclosableInbound struct {
	Tag    string `json:"tag,omitempty"`
	Reason string `json:"reason"`
	// Node is set by a master merging the answer a node returned; this package
	// never fills it.
	Node string `json:"node,omitempty"`
}

// DisconnectResult is what disconnecting one user managed to do.
type DisconnectResult struct {
	// Connections counts the routed connections closed.
	Connections int `json:"connections"`
	// Sessions counts the multiplex / anytls sessions cut outright.
	Sessions int `json:"sessions"`
	// Unclosable is len(UnclosableInbounds), kept for masters that predate the
	// list: they read only this count, and warn when it is not zero.
	Unclosable int `json:"unclosable"`
	// UnclosableInbounds names the inbounds where the user is likely still
	// connected by a session this layer cannot reach. Their routed connections
	// are closed, but the client opens new streams on the session it still has.
	UnclosableInbounds []UnclosableInbound `json:"unclosableInbounds,omitempty"`
	// LiveInbounds are the inbounds the user had routed connections on just
	// before the kick, sorted -- the evidence the report rests on, handed on so
	// the service layer can look for multiplexed Shadowsocks among them. Not
	// sent: it describes connections that are closed by the time anyone reads it.
	LiveInbounds []string `json:"-"`
}

// AddUnclosable records one inbound left with a session that could not be cut.
func (r *DisconnectResult) AddUnclosable(tag string, reason string) {
	r.UnclosableInbounds = append(r.UnclosableInbounds, UnclosableInbound{Tag: tag, Reason: reason})
	r.Unclosable++
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
	var result DisconnectResult
	box, err := c.running()
	if err != nil {
		return result, err
	}
	// Read before anything is closed: the routed connections are the only sign
	// of which inbounds the user is on right now, and the kick closes them.
	live := box.ConnTracker().UserInbounds(user)
	result.LiveInbounds = make([]string, 0, len(live))
	for tag := range live {
		result.LiveInbounds = append(result.LiveInbounds, tag)
	}
	sort.Strings(result.LiveInbounds)

	kickInbounds(&result, box.Inbound().Inbounds(), user, live)
	result.Connections = box.ConnTracker().CloseConnByUser(user)
	return result, nil
}

// kickInbounds cuts user's sessions on every inbound that can reach them, and
// records an inbound as unclosable only when live shows the user on it.
//
// For QUIC the registry knows only that the user has been seen on the inbound,
// ever, which is right for a removal and wrong as a report on a kick: one
// connection to a QUIC inbound used to make every later kick warn about it for
// as long as the inbound lived, and the warning tells the operator to disable
// the client (#234). Requiring a routed connection a moment ago trades that for
// the opposite miss -- a QUIC session with no stream open at this instant goes
// unreported. For a kick that is the cheap side: the client may reconnect at
// once anyway, so the session it keeps buys it nothing it could not get back.
//
// Sessions first: cutting a carrier ends the streams on it, so fewer of them
// are left for the tracker to close one by one.
func kickInbounds(result *DisconnectResult, inbounds []adapter.Inbound, user string, live map[string]struct{}) {
	for _, inbound := range inbounds {
		kicker, ok := inbound.(userKicker)
		if !ok {
			continue
		}
		r := kicker.KickUser(user)
		result.Sessions += r.Cut
		if r.Unclosable == 0 {
			continue
		}
		if _, ok := live[inbound.Tag()]; ok {
			result.AddUnclosable(inbound.Tag(), UnclosableQUIC)
		}
	}
}
