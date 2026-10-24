package connectors

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/secrets"
)

// Field describes one config field of a kind (CON-01); the web app renders forms from it.
type Field struct {
	Name     string
	Label    string
	Type     dto.FieldType
	Required bool
	Help     string
	// Default is applied when the field is absent (never for secrets).
	Default any
	// Endpoint marks a field naming the server the connector talks to (base_url, api_base,
	// outbound_url). Whoever controls that server chooses the sender ids the connector
	// reports, so changing it deletes the identities linked through the connector (S5).
	Endpoint bool
	// Generated marks a field Kind.Prepare fills when a new connector leaves it empty (the
	// webhook secret). Forms may leave it empty; a PATCH sending it empty keeps the stored
	// value instead of clearing it.
	Generated bool
}

// EndpointChanged reports whether an endpoint field differs between two configs.
func (k Kind) EndpointChanged(before, after Config) bool {
	for _, f := range k.Fields {
		if f.Endpoint && before.String(f.Name) != after.String(f.Name) {
			return true
		}
	}
	return false
}

// Secret reports whether the field holds a secret (CON-03).
func (f Field) Secret() bool { return f.Type == dto.FieldTypeSecret }

// Kind is a supported platform: its config schema and its constructor.
type Kind struct {
	Name   string
	Label  string
	Fields []Field
	// CanListChats tells whether its connectors implement ChatLister (CON-09).
	CanListChats bool
	// InboundHook tells whether its connectors receive messages at POST /api/v1/hooks/{id};
	// the API then reports that URL as the connector's inbound_url.
	InboundHook bool
	// LinkAdminsOnly tells that whoever holds the connector's credentials chooses the sender
	// ids of its messages (the webhook): pairing commands then link only codes of the admins of
	// the connection's organization (IDN-05, AD-008 as amended by AD-016), and only they are given
	// such a code, so nobody else's identity can be claimed through it.
	LinkAdminsOnly bool
	// New builds a connector from a validated config.
	New func(cfg Config, d Deps) (Connector, error)
	// Prepare, when set, completes the config of a new connector before validation (e.g.
	// generates a secret left empty) and returns the names of the fields it generated.
	// Generated secrets are shown once, in the create response.
	Prepare func(cfg Config) (Config, []string)
}

// Registry holds the supported kinds by name.
type Registry struct {
	kinds map[string]Kind
}

// NewRegistry returns a registry holding kinds.
func NewRegistry(kinds ...Kind) *Registry {
	r := &Registry{kinds: map[string]Kind{}}
	for _, k := range kinds {
		r.Register(k)
	}
	return r
}

// Register adds a kind; an empty or duplicate name panics (a programming error).
func (r *Registry) Register(k Kind) {
	if k.Name == "" {
		panic("connectors: kind without a name")
	}
	if _, dup := r.kinds[k.Name]; dup {
		panic("connectors: kind registered twice: " + k.Name)
	}
	r.kinds[k.Name] = k
}

// Kinds returns every kind ordered by name.
func (r *Registry) Kinds() []Kind {
	out := make([]Kind, 0, len(r.kinds))
	for _, k := range r.kinds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the kind named name.
func (r *Registry) Lookup(name string) (Kind, bool) {
	k, ok := r.kinds[name]
	return k, ok
}

// Config is a connector configuration: field name → value (string, bool or []string).
type Config map[string]any

// String returns a string field, or "".
func (c Config) String(name string) string {
	s, _ := c[name].(string)
	return s
}

// Bool returns a bool field, or false.
func (c Config) Bool(name string) bool {
	b, _ := c[name].(bool)
	return b
}

// Strings returns a string_list field, or an empty list.
func (c Config) Strings(name string) []string {
	out := []string{}
	switch v := c[name].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (k Kind) field(name string) (Field, bool) {
	for _, f := range k.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Validate checks cfg against the schema (CON-02) and returns it normalized: strings
// trimmed, empty values dropped, lists as []string and defaults applied. errs maps each
// rejected field to its reason ("required", "invalid url", "unknown field", ...).
func (k Kind) Validate(cfg Config) (Config, map[string]string) {
	out := Config{}
	errs := map[string]string{}
	for name := range cfg {
		if _, ok := k.field(name); !ok {
			errs[name] = "unknown field"
		}
	}
	for _, f := range k.Fields {
		v, reason := normalize(f, cfg[f.Name])
		switch {
		case reason != "":
			errs[f.Name] = reason
		case v != nil:
			out[f.Name] = v
		case f.Default != nil && !f.Secret():
			out[f.Name] = f.Default
		case f.Required:
			errs[f.Name] = "required"
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// normalize returns the field's value in canonical form, nil when absent or empty, or the
// reason it is invalid.
func normalize(f Field, v any) (any, string) {
	if v == nil {
		return nil, ""
	}
	switch f.Type {
	case dto.FieldTypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, "must be true or false"
		}
		return b, ""
	case dto.FieldTypeStringList:
		var items []any
		switch l := v.(type) {
		case []any:
			items = l
		case []string:
			for _, s := range l {
				items = append(items, s)
			}
		default:
			return nil, "must be a list of strings"
		}
		list := []string{}
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, "must be a list of strings"
			}
			if s = strings.TrimSpace(s); s != "" {
				list = append(list, s)
			}
		}
		if len(list) == 0 {
			return nil, ""
		}
		return list, ""
	default: // string, url, secret
		s, ok := v.(string)
		if !ok {
			return nil, "must be a string"
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, ""
		}
		if f.Type == dto.FieldTypeURL && !validURL(s) {
			return nil, "invalid url"
		}
		return s, ""
	}
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// Merge applies a PATCH to a stored config and returns the result (unvalidated): present
// keys replace stored values, null removes a field, a secret sent as dto.SecretMask keeps
// its stored value (CON-03) and so does a generated field sent empty. stored is not modified.
func (k Kind) Merge(stored, patch Config) Config {
	out := Config{}
	for name, v := range stored {
		out[name] = v
	}
	for name, v := range patch {
		f, _ := k.field(name)
		switch {
		case v == nil:
			delete(out, name)
		case f.Secret() && v == dto.SecretMask:
			// keep the stored secret
		case f.Generated && blank(v):
			// keep the stored value: an empty input never clears a generated field
		default:
			out[name] = v
		}
	}
	return out
}

func blank(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// Masked returns the config as the API shows it: secrets that are set read as
// dto.SecretMask, unset secrets are absent (CON-03).
func (k Kind) Masked(cfg Config) map[string]any {
	out := map[string]any{}
	for name, v := range cfg {
		if f, ok := k.field(name); ok && f.Secret() {
			if v != nil && v != "" {
				out[name] = dto.SecretMask
			}
			continue
		}
		out[name] = v
	}
	return out
}

// Encode splits a validated config into the public JSON (connectors.config_json) and the
// secrets sealed with box (connectors.secrets_enc, nil when no secret is set) (SEC-04).
func (k Kind) Encode(box *secrets.Box, cfg Config) (public string, sealed []byte, err error) {
	pub := Config{}
	sec := map[string]string{}
	for name, v := range cfg {
		if f, ok := k.field(name); ok && f.Secret() {
			if s, _ := v.(string); s != "" {
				sec[name] = s
			}
			continue
		}
		pub[name] = v
	}
	data, err := json.Marshal(pub)
	if err != nil {
		return "", nil, err
	}
	if len(sec) == 0 {
		return string(data), nil, nil
	}
	plain, err := json.Marshal(sec)
	if err != nil {
		return "", nil, err
	}
	sealed, err = box.Seal(plain)
	if err != nil {
		return "", nil, err
	}
	return string(data), sealed, nil
}

// Decode rebuilds the full config of a stored connector, opening its secrets with box.
func Decode(box *secrets.Box, public string, sealed []byte) (Config, error) {
	cfg := Config{}
	if public != "" {
		if err := json.Unmarshal([]byte(public), &cfg); err != nil {
			return nil, fmt.Errorf("connector config: %w", err)
		}
	}
	if len(sealed) == 0 {
		return cfg, nil
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, fmt.Errorf("connector secrets: %w", err)
	}
	sec := map[string]string{}
	if err := json.Unmarshal(plain, &sec); err != nil {
		return nil, fmt.Errorf("connector secrets: %w", err)
	}
	for name, v := range sec {
		cfg[name] = v
	}
	return cfg, nil
}
