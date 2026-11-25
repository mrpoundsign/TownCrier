package pluginhost

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/mrpoundsign/TownCrier/pkg/config"
	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
	"github.com/mrpoundsign/TownCrier/pkg/secrets"
)

func pluginSecretKey(pluginUUID, fieldKey string) string {
	return "plugin:" + strings.TrimSpace(pluginUUID) + ":" + strings.TrimSpace(fieldKey)
}

func schemaPasswordKeys(schema []*pluginv1.SettingField) map[string]bool {
	out := make(map[string]bool)
	for _, f := range schema {
		if f != nil && f.GetType() == pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD && f.GetKey() != "" {
			out[f.GetKey()] = true
		}
	}
	return out
}

func schemaFieldTypes(schema []*pluginv1.SettingField) map[string]pluginv1.SettingFieldType {
	out := make(map[string]pluginv1.SettingFieldType, len(schema))
	for _, f := range schema {
		if f != nil && f.GetKey() != "" {
			out[f.GetKey()] = f.GetType()
		}
	}
	return out
}

// GetGlobalSettings reads a plugin's persisted settings merged with keyring password fields.
func (m *Manager) GetGlobalSettings(pluginUUID string) *pluginv1.SettingsMap {
	uuid := strings.TrimSpace(pluginUUID)
	if uuid == "" {
		return &pluginv1.SettingsMap{Values: map[string]*pluginv1.SettingValue{}}
	}

	var rawMap map[string]any
	if cfg := m.currentConfig(); cfg != nil && cfg.Plugins.PluginSettings != nil {
		rawMap = maps.Clone(cfg.Plugins.PluginSettings[uuid])
	}
	if rawMap == nil {
		rawMap = make(map[string]any)
	}

	var schema []*pluginv1.SettingField
	m.mu.RLock()
	if ent := m.entries[uuid]; ent != nil && ent.manifest != nil {
		schema = ent.manifest.GetGlobalSettingsSchema()
	}
	m.mu.RUnlock()

	if m.opts.Secrets != nil {
		for _, f := range schema {
			if f == nil || f.GetType() != pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD {
				continue
			}
			key := f.GetKey()
			if sec, err := m.opts.Secrets.Get(pluginSecretKey(uuid, key)); err == nil && sec != "" {
				rawMap[key] = sec
			}
		}
	}

	return mapToSettingsProto(rawMap, schema)
}

// SaveGlobalSettings atomically persists plugin settings, routing password fields to secrets.Store.
func (m *Manager) SaveGlobalSettings(pluginUUID string, settings *pluginv1.SettingsMap) error {
	uuid := strings.TrimSpace(pluginUUID)
	if uuid == "" {
		return errors.New("plugin UUID cannot be empty")
	}

	var schema []*pluginv1.SettingField
	m.mu.RLock()
	if ent := m.entries[uuid]; ent != nil && ent.manifest != nil {
		schema = ent.manifest.GetGlobalSettingsSchema()
	}
	m.mu.RUnlock()

	passKeys := schemaPasswordKeys(schema)
	incoming := settingsProtoToMap(settings)
	persisted := make(map[string]any, len(incoming))

	for k, v := range incoming {
		if passKeys[k] {
			strVal := strings.TrimSpace(fmt.Sprint(v))
			if m.opts.Secrets != nil {
				if strVal == "" {
					_ = m.opts.Secrets.Delete(pluginSecretKey(uuid, k))
				} else if err := m.opts.Secrets.Set(pluginSecretKey(uuid, k), strVal); err != nil {
					return fmt.Errorf("store plugin secret %q: %w", k, err)
				}
			}
			continue
		}
		persisted[k] = v
	}

	if m.opts.Callbacks.UpdateConfig != nil {
		if err := m.opts.Callbacks.UpdateConfig(func(c *config.Config) {
			if c.Plugins.PluginSettings == nil {
				c.Plugins.PluginSettings = make(map[string]map[string]any)
			}
			c.Plugins.PluginSettings[uuid] = persisted
		}); err != nil {
			return err
		}
	}

	m.notifyStatusChange()
	return nil
}

// SaveGlobalSettingsFromUI updates settings from the UI form (preserving untouched password fields when omitted
// and preserving plugin-managed info/link/button metadata keys) and notifies the running plugin.
func (m *Manager) SaveGlobalSettingsFromUI(pluginUUID string, formValues map[string]any, clearSecrets []string) error {
	uuid := strings.TrimSpace(pluginUUID)
	if uuid == "" {
		return errors.New("plugin UUID cannot be empty")
	}

	var schema []*pluginv1.SettingField
	m.mu.RLock()
	if ent := m.entries[uuid]; ent != nil && ent.manifest != nil {
		schema = ent.manifest.GetGlobalSettingsSchema()
	}
	m.mu.RUnlock()

	fieldTypes := schemaFieldTypes(schema)
	clearSet := make(map[string]bool, len(clearSecrets))
	for _, k := range clearSecrets {
		clearSet[k] = true
	}

	if m.opts.Secrets != nil {
		for k, ft := range fieldTypes {
			if ft != pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD {
				continue
			}
			if clearSet[k] {
				_ = m.opts.Secrets.Delete(pluginSecretKey(uuid, k))
				continue
			}
			if raw, ok := formValues[k]; ok {
				s := strings.TrimSpace(fmt.Sprint(raw))
				if s != "" {
					if err := m.opts.Secrets.Set(pluginSecretKey(uuid, k), s); err != nil {
						return fmt.Errorf("save secret %q: %w", k, err)
					}
				}
			}
		}
	}

	if m.opts.Callbacks.UpdateConfig != nil {
		if err := m.opts.Callbacks.UpdateConfig(func(c *config.Config) {
			if c.Plugins.PluginSettings == nil {
				c.Plugins.PluginSettings = make(map[string]map[string]any)
			}
			existing := maps.Clone(c.Plugins.PluginSettings[uuid])
			if existing == nil {
				existing = make(map[string]any)
			}
			for k, v := range formValues {
				ft := fieldTypes[k]
				if ft == pluginv1.SettingFieldType_SETTING_FIELD_TYPE_PASSWORD ||
					ft == pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO ||
					ft == pluginv1.SettingFieldType_SETTING_FIELD_TYPE_LINK ||
					ft == pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON {
					continue
				}
				existing[k] = v
			}
			c.Plugins.PluginSettings[uuid] = existing
		}); err != nil {
			return err
		}
	}

	merged := m.GetGlobalSettings(uuid)
	return m.notifyGlobalSettings(uuid, merged)
}

// HasSecret reports whether a password field currently has a non-empty value stored in secrets.Store.
func (m *Manager) HasSecret(pluginUUID, fieldKey string) bool {
	if m.opts.Secrets == nil {
		return false
	}
	val, err := m.opts.Secrets.Get(pluginSecretKey(pluginUUID, fieldKey))
	return err == nil && val != ""
}

func mapToSettingsProto(raw map[string]any, schema []*pluginv1.SettingField) *pluginv1.SettingsMap {
	types := schemaFieldTypes(schema)
	values := make(map[string]*pluginv1.SettingValue, len(raw))
	for k, v := range raw {
		if v == nil {
			continue
		}
		if sv := anyToSettingValue(v, types[k]); sv != nil {
			values[k] = sv
		}
	}
	return &pluginv1.SettingsMap{Values: values}
}

func anyToSettingValue(v any, ft pluginv1.SettingFieldType) *pluginv1.SettingValue {
	switch x := v.(type) {
	case string:
		switch ft {
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO:
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_InfoVal{InfoVal: &pluginv1.InfoValue{Text: x}}}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_LINK:
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_LinkVal{LinkVal: &pluginv1.LinkValue{Url: x}}}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON:
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_ButtonVal{ButtonVal: &pluginv1.ButtonValue{Label: x}}}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_NUMBER:
			if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_NumberVal{NumberVal: f}}
			}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BOOL:
			if b, err := strconv.ParseBool(strings.TrimSpace(x)); err == nil {
				return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: b}}
			}
		}
		return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_StringVal{StringVal: x}}
	case bool:
		return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_BoolVal{BoolVal: x}}
	case int:
		return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_NumberVal{NumberVal: float64(x)}}
	case int64:
		return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_NumberVal{NumberVal: float64(x)}}
	case float64:
		return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_NumberVal{NumberVal: x}}
	case map[string]any:
		switch ft {
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_INFO:
			copyable, _ := x["copyable"].(bool)
			txt := ""
			if rawTxt, ok := x["text"]; ok && rawTxt != nil {
				txt = fmt.Sprint(rawTxt)
			}
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_InfoVal{InfoVal: &pluginv1.InfoValue{Text: txt, Copyable: copyable}}}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_LINK:
			u, _ := x["url"].(string)
			lbl, _ := x["label"].(string)
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_LinkVal{LinkVal: &pluginv1.LinkValue{Url: u, Label: lbl}}}
		case pluginv1.SettingFieldType_SETTING_FIELD_TYPE_BUTTON:
			lbl, _ := x["label"].(string)
			dis, _ := x["disabled"].(bool)
			return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_ButtonVal{ButtonVal: &pluginv1.ButtonValue{Label: lbl, Disabled: dis}}}
		}
	}
	return &pluginv1.SettingValue{Kind: &pluginv1.SettingValue_StringVal{StringVal: fmt.Sprint(v)}}
}

func settingsProtoToMap(m *pluginv1.SettingsMap) map[string]any {
	if m == nil || len(m.GetValues()) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(m.GetValues()))
	for k, sv := range m.GetValues() {
		if sv == nil {
			continue
		}
		switch v := sv.Kind.(type) {
		case *pluginv1.SettingValue_StringVal:
			out[k] = v.StringVal
		case *pluginv1.SettingValue_NumberVal:
			out[k] = v.NumberVal
		case *pluginv1.SettingValue_BoolVal:
			out[k] = v.BoolVal
		case *pluginv1.SettingValue_InfoVal:
			if v.InfoVal != nil {
				out[k] = map[string]any{
					"text":     v.InfoVal.GetText(),
					"copyable": v.InfoVal.GetCopyable(),
				}
			}
		case *pluginv1.SettingValue_LinkVal:
			if v.LinkVal != nil {
				out[k] = map[string]any{
					"url":   v.LinkVal.GetUrl(),
					"label": v.LinkVal.GetLabel(),
				}
			}
		case *pluginv1.SettingValue_ButtonVal:
			if v.ButtonVal != nil {
				out[k] = map[string]any{
					"label":    v.ButtonVal.GetLabel(),
					"disabled": v.ButtonVal.GetDisabled(),
				}
			}
		}
	}
	return out
}

// Keep secrets import used cleanly across build configurations.
var _ = secrets.ErrNotFound
