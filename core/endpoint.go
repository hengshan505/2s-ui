package core

import (
	"encoding/json"

	"github.com/shenaba/2s-ui/logger"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

// Every method here takes the live box once, through running(), and then works
// from that one reference.
//
// The managers come off the box rather than from package-level vars, so a
// hot-reload cannot apply an add to a manager belonging to a box that has since
// been closed by a restart -- and reading c.isRunning directly, which is what
// these did, was a data race against Start and Stop besides.

func inboundRecipe(box *Box, config []byte) (recipe, error) {
	var inbound_config option.Inbound
	if err := inbound_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return recipe{}, err
	}
	return recipe{
		ctx:           box.ctx,
		router:        box.Router(),
		logger:        box.LogFactory().NewLogger("inbound/" + inbound_config.Type + "[" + inbound_config.Tag + "]"),
		tag:           inbound_config.Tag,
		componentType: inbound_config.Type,
		options:       inbound_config.Options,
	}, nil
}

func (c *Core) AddInbound(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := inboundRecipe(box, config)
	if err != nil {
		return err
	}
	return box.inbound.create(r)
}

// RenameInbound replaces inbound oldTag with config, which names another tag.
// A rename that does not start leaves the old inbound running (see
// componentTable.rename); Remove then Add left none.
func (c *Core) RenameInbound(oldTag string, config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := inboundRecipe(box, config)
	if err != nil {
		return err
	}
	logger.Info("rename inbound: ", oldTag, " -> ", r.tag)
	return box.inbound.rename(oldTag, r)
}

func (c *Core) RemoveInbound(tag string) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	logger.Info("remove inbound: ", tag)
	return box.inbound.Remove(tag)
}

func (c *Core) AddOutbound(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	var outbound_config option.Outbound
	if err = outbound_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return err
	}

	outboundCtx := adapter.WithContext(box.ctx, &adapter.InboundContext{
		Outbound: outbound_config.Tag,
	})

	return box.Outbound().Create(
		outboundCtx,
		box.Router(),
		box.LogFactory().NewLogger("outbound/"+outbound_config.Type+"["+outbound_config.Tag+"]"),
		outbound_config.Tag,
		outbound_config.Type,
		outbound_config.Options)
}

func (c *Core) RemoveOutbound(tag string) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	logger.Info("remove outbound: ", tag)
	return box.outbound.Remove(tag)
}

func endpointRecipe(box *Box, config []byte) (recipe, error) {
	var endpoint_config option.Endpoint
	if err := endpoint_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return recipe{}, err
	}
	return recipe{
		ctx:           box.ctx,
		router:        box.Router(),
		logger:        box.LogFactory().NewLogger("endpoint/" + endpoint_config.Type + "[" + endpoint_config.Tag + "]"),
		tag:           endpoint_config.Tag,
		componentType: endpoint_config.Type,
		options:       endpoint_config.Options,
	}, nil
}

func (c *Core) AddEndpoint(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := endpointRecipe(box, config)
	if err != nil {
		return err
	}
	return box.endpoint.create(r)
}

// RenameEndpoint is RenameInbound for an endpoint. One something depends on,
// or that route.final names, is refused like a removal.
func (c *Core) RenameEndpoint(oldTag string, config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := endpointRecipe(box, config)
	if err != nil {
		return err
	}
	logger.Info("rename endpoint: ", oldTag, " -> ", r.tag)
	return box.endpoint.rename(oldTag, r)
}

func (c *Core) RemoveEndpoint(tag string) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	logger.Info("remove endpoint: ", tag)
	return box.endpoint.Remove(tag)
}

func serviceRecipe(box *Box, config []byte) (recipe, error) {
	var srv_config option.Service
	if err := srv_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return recipe{}, err
	}
	return recipe{
		ctx:           box.ctx,
		logger:        box.LogFactory().NewLogger("service/" + srv_config.Type + "[" + srv_config.Tag + "]"),
		tag:           srv_config.Tag,
		componentType: srv_config.Type,
		options:       srv_config.Options,
	}, nil
}

func (c *Core) AddService(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := serviceRecipe(box, config)
	if err != nil {
		return err
	}
	return box.service.create(r)
}

// RenameService is RenameInbound for a service.
func (c *Core) RenameService(oldTag string, config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	r, err := serviceRecipe(box, config)
	if err != nil {
		return err
	}
	logger.Info("rename service: ", oldTag, " -> ", r.tag)
	return box.service.rename(oldTag, r)
}

func (c *Core) RemoveService(tag string) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	logger.Info("remove service: ", tag)
	return box.service.Remove(tag)
}

// ValidateOutbound vets config as far as can be done without touching the
// running core: it parses it, and checks that what it depends on exists and
// does not lead back to it. It is for an edit applied by a restart instead of
// in place, which nothing else vets before the commit -- and a cycle or a
// missing detour there would fail the whole core on the restart rather than
// just the save. What only shows at Start (a port already taken) still gets
// through.
//
// It does not build the outbound. Building is not free of side effects: a
// wireguard endpoint creates its gVisor stack in its constructor and registers
// the cleanup for it only in Start, so one built here and dropped would leak
// the stack for the life of the process.
func (c *Core) ValidateOutbound(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	var outbound_config option.Outbound
	if err = outbound_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return err
	}
	dependencies, err := dependenciesOf(config)
	if err != nil {
		return err
	}
	return box.outbound.checkDependencies(outbound_config.Tag, dependencies)
}

// ValidateEndpoint is ValidateOutbound for an endpoint.
func (c *Core) ValidateEndpoint(config []byte) error {
	box, err := c.running()
	if err != nil {
		return err
	}
	var endpoint_config option.Endpoint
	if err = endpoint_config.UnmarshalJSONContext(box.ctx, config); err != nil {
		return err
	}
	dependencies, err := dependenciesOf(config)
	if err != nil {
		return err
	}
	return box.outbound.checkDependencies(endpoint_config.Tag, dependencies)
}

// dependenciesOf reads off a config what the outbound or endpoint built from
// it would report as its Dependencies, which in 1.14.3 is exactly two things:
// the dialer's detour (outbound.NewAdapterWithDialerOptions, and endpoint's)
// and a selector's or urltest's outbounds (protocol/group). Every other
// adapter is created with none.
func dependenciesOf(config []byte) ([]string, error) {
	var fields struct {
		Type      string   `json:"type"`
		Detour    string   `json:"detour"`
		Outbounds []string `json:"outbounds"`
	}
	if err := json.Unmarshal(config, &fields); err != nil {
		return nil, err
	}
	var dependencies []string
	if fields.Detour != "" {
		dependencies = append(dependencies, fields.Detour)
	}
	if fields.Type == C.TypeSelector || fields.Type == C.TypeURLTest {
		dependencies = append(dependencies, fields.Outbounds...)
	}
	return dependencies, nil
}

// HasOutbound reports whether the running core holds outbound tag -- in the
// outbound table itself, not an endpoint of that name.
func (c *Core) HasOutbound(tag string) bool {
	box, err := c.running()
	if err != nil {
		return false
	}
	_, found := box.outbound.get(tag)
	return found
}

// HasEndpoint reports whether the running core holds endpoint tag.
func (c *Core) HasEndpoint(tag string) bool {
	box, err := c.running()
	if err != nil {
		return false
	}
	_, found := box.endpoint.get(tag)
	return found
}

// OutboundDependents names what depends on outbound tag in the running core.
// An outbound something depends on cannot be replaced in place: a selector
// resolved it at Start and would keep the closed instance (core/managers.go).
func (c *Core) OutboundDependents(tag string) []string {
	box, err := c.running()
	if err != nil {
		return nil
	}
	return box.outbound.dependents(tag)
}

// EndpointDependents names what depends on endpoint tag in the running core.
// An endpoint something depends on cannot be replaced in place: a selector
// resolved it at Start and would keep the closed instance (core/managers.go).
func (c *Core) EndpointDependents(tag string) []string {
	box, err := c.running()
	if err != nil {
		return nil
	}
	return box.endpoint.Dependents(tag)
}
