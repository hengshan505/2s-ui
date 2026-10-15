package database

import (
	"encoding/json"
	"log"

	"github.com/shenaba/2s-ui/database/model"
	"github.com/shenaba/2s-ui/util"

	"gorm.io/gorm"
)

// migratedKeyExplicitHTTPClients marks that every stored remote rule-set has
// been given an explicit way to download.
const migratedKeyExplicitHTTPClients = "migratedExplicitHTTPClients"

// migrateExplicitRuleSetHTTPClients writes out, on every remote rule-set, the
// download path it is already taking, so that none of them is left on one of
// the two forms sing-box 1.14 deprecated and 1.16 removes:
//
//   - download_detour, which the frontend kept writing after the 1.14 migration
//     converted the stored ones. It becomes the equivalent http_client. A
//     detour to an optionless direct outbound becomes a direct client: the
//     legacy field skipped sing-box's empty-direct check, so it really did
//     download directly, and a detour cannot say that (util.DirectHTTPClient).
//   - no client at all, with no default client declared, which falls back to
//     the implicit client riding the default outbound. Until now the panel
//     papered over that when the config was generated, declaring a client on
//     route.final. It now names that same outbound on the rule-set itself --
//     route.final, or with none set, the first outbound, which is the one the
//     core takes as its default -- so nothing downloads any differently.
//
// "Already taking" is the point. Some of these rule-sets used to download
// directly and were moved onto the default outbound by repairRuleSetHTTPClients,
// which dropped a detour to an optionless direct outbound and with it the only
// trace of the old choice. That cannot be told apart from a rule-set that never
// named a client, so both keep the path they have now rather than being guessed
// back.
//
// From here on ConfigService.Save refuses a config that would put either form
// back, and the panel no longer declares a fallback client of its own.
func migrateExplicitRuleSetHTTPClients() error {
	var flag model.Setting
	err := db.Where("key = ?", migratedKeyExplicitHTTPClients).First(&flag).Error
	if err == nil {
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		changed, err := migrateExplicitRuleSetHTTPClientsIn(tx)
		if err != nil {
			return err
		}
		if changed > 0 {
			log.Printf("http clients: made %d rule-set download path(s) explicit", changed)
		}
		return tx.Create(&model.Setting{Key: migratedKeyExplicitHTTPClients, Value: "true"}).Error
	})
}

func migrateExplicitRuleSetHTTPClientsIn(tx *gorm.DB) (int, error) {
	var setting model.Setting
	err := tx.Where("key = ?", "config").First(&setting).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var root map[string]json.RawMessage
	if err = json.Unmarshal([]byte(setting.Value), &root); err != nil {
		log.Printf("http clients: skipping, cannot parse config: %v", err)
		return 0, nil
	}
	rawRoute, ok := root["route"]
	if !ok || len(rawRoute) == 0 {
		return 0, nil
	}
	var route map[string]any
	if err = json.Unmarshal(rawRoute, &route); err != nil {
		return 0, nil
	}
	// Only http_clients and the route decide whether a default client exists,
	// so only those two are decoded; the rest of the config is carried through
	// as it was written.
	var clients []any
	if raw, has := root["http_clients"]; has {
		_ = json.Unmarshal(raw, &clients)
	}
	hasDefault := util.HasDefaultHTTPClient(map[string]any{"route": route, "http_clients": clients})

	noopDetours, err := plainDirectOutboundTags(tx)
	if err != nil {
		return 0, err
	}
	clientFor := func(detour string) map[string]any {
		if _, noop := noopDetours[detour]; noop {
			return util.DirectHTTPClient()
		}
		return map[string]any{"detour": detour}
	}

	ruleSets, _ := route["rule_set"].([]any)
	changed := 0
	var fallback map[string]any
	fallbackResolved := false
	for _, entry := range ruleSets {
		ruleSet, isObject := entry.(map[string]any)
		if !isObject {
			continue
		}
		if ruleSetType, _ := ruleSet["type"].(string); ruleSetType != "remote" {
			continue
		}
		tag, _ := ruleSet["tag"].(string)
		if raw, hasDetour := ruleSet["download_detour"]; hasDetour {
			delete(ruleSet, "download_detour")
			changed++
			// The two conflict in 1.14 and the client is what the 1.14
			// migration kept as well, so a set client wins.
			if !util.IsEmptyHTTPClient(ruleSet["http_client"]) {
				continue
			}
			if detour, _ := raw.(string); detour != "" {
				ruleSet["http_client"] = clientFor(detour)
				continue
			}
		}
		if hasDefault || !util.IsEmptyHTTPClient(ruleSet["http_client"]) {
			continue
		}
		if !fallbackResolved {
			fallback, err = defaultDownloadClient(tx, route, clientFor)
			if err != nil {
				return 0, err
			}
			fallbackResolved = true
		}
		if fallback == nil {
			log.Printf("http clients: rule-set %q names no HTTP client and route.final names no outbound; set one by hand", tag)
			continue
		}
		ruleSet["http_client"] = cloneClient(fallback)
		changed++
	}
	if changed == 0 {
		return 0, nil
	}

	encodedRoute, err := json.Marshal(route)
	if err != nil {
		return 0, err
	}
	root["route"] = encodedRoute
	encodedRoot, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return 0, err
	}
	setting.Value = string(encodedRoot)
	if err = tx.Save(&setting).Error; err != nil {
		return 0, err
	}
	return changed, nil
}

// defaultDownloadClient is the client that dials what the implicit default
// client dials: the core's default outbound. That is route.final when it is
// set, and otherwise the first outbound the core creates, which is the first
// row in the order the config is generated from (OutboundService.GetAllConfig
// scans the table unordered, as here). nil means route.final names nothing the
// config defines -- sing-box refuses to start on that by itself, and naming it
// on every rule-set as well would only spread the broken reference.
func defaultDownloadClient(tx *gorm.DB, route map[string]any, clientFor func(string) map[string]any) (map[string]any, error) {
	if final, _ := route["final"].(string); final != "" {
		// route.final may name an endpoint as well as an outbound: sing-box
		// resolves the default through outbound.Manager.Outbound, which falls
		// back to the endpoints.
		var count int64
		if err := tx.Model(model.Outbound{}).Where("tag = ?", final).Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			if err := tx.Model(model.Endpoint{}).Where("tag = ?", final).Count(&count).Error; err != nil {
				return nil, err
			}
		}
		if count == 0 {
			return nil, nil
		}
		return clientFor(final), nil
	}
	var outbounds []model.Outbound
	if err := tx.Model(model.Outbound{}).Scan(&outbounds).Error; err != nil {
		return nil, err
	}
	for _, outbound := range outbounds {
		if outbound.Tag != "" {
			return clientFor(outbound.Tag), nil
		}
	}
	// A core with no outbounds falls back to a direct one of its own.
	return util.DirectHTTPClient(), nil
}

func cloneClient(client map[string]any) map[string]any {
	copied := make(map[string]any, len(client))
	for key, value := range client {
		copied[key] = value
	}
	return copied
}
