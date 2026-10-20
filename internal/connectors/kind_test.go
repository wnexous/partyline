package connectors

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/secrets"
)

// testKind has one field of every type.
func testKind() Kind {
	return Kind{
		Name: "sample", Label: "Sample",
		Fields: []Field{
			{Name: "base_url", Label: "Server URL", Type: dto.FieldTypeURL, Required: true, Help: "https://..."},
			{Name: "api_key", Label: "API key", Type: dto.FieldTypeSecret, Required: true},
			{Name: "instances", Label: "Instances", Type: dto.FieldTypeStringList},
			{Name: "verbose", Label: "Verbose", Type: dto.FieldTypeBool, Default: false},
			{Name: "region", Label: "Region", Type: dto.FieldTypeString, Default: "eu"},
			{Name: "extra_key", Label: "Extra key", Type: dto.FieldTypeSecret},
		},
	}
}

func testBox(t *testing.T) *secrets.Box {
	t.Helper()
	box, err := secrets.NewBox(bytes.Repeat([]byte{7}, secrets.KeySize))
	require.NoError(t, err)
	return box
}

const syntheticKey = "sk-test-0123456789abcdef"

func validConfig() Config {
	return Config{"base_url": "https://evolution.example.test", "api_key": syntheticKey}
}

func TestRegistryListsAndLooksUpKinds(t *testing.T) {
	a, b := testKind(), Kind{Name: "other", Label: "Other"}
	r := NewRegistry(a, b)
	names := []string{}
	for _, k := range r.Kinds() {
		names = append(names, k.Name)
	}
	require.Equal(t, []string{"other", "sample"}, names, "kinds are listed by name")

	got, ok := r.Lookup("sample")
	require.True(t, ok)
	require.Equal(t, "Sample", got.Label)
	_, ok = r.Lookup("missing")
	require.False(t, ok)
	require.Panics(t, func() { r.Register(testKind()) }, "duplicate kind names are a programming error")
}

// CON-01: each field is described with name, label, type, required, secret and help.
func TestFieldSecretFollowsType(t *testing.T) {
	k := testKind()
	require.True(t, k.Fields[1].Secret())
	require.False(t, k.Fields[0].Secret())
}

// CON-02: a missing required field or a field failing its type is named with its reason.
func TestValidateNamesMissingAndInvalidFields(t *testing.T) {
	k := testKind()

	_, errs := k.Validate(Config{})
	require.Equal(t, map[string]string{"base_url": "required", "api_key": "required"}, errs)

	_, errs = k.Validate(Config{"base_url": "not a url", "api_key": syntheticKey})
	require.Equal(t, map[string]string{"base_url": "invalid url"}, errs)

	_, errs = k.Validate(Config{"base_url": "ftp://example.test", "api_key": syntheticKey})
	require.Equal(t, map[string]string{"base_url": "invalid url"}, errs, "only http and https")

	_, errs = k.Validate(Config{"base_url": "  ", "api_key": syntheticKey})
	require.Equal(t, map[string]string{"base_url": "required"}, errs, "blank strings count as missing")

	cfg := validConfig()
	cfg["verbose"] = "yes"
	cfg["instances"] = []any{"main", 3}
	cfg["region"] = 12
	cfg["unknown"] = "x"
	_, errs = k.Validate(cfg)
	require.Equal(t, map[string]string{
		"verbose":   "must be true or false",
		"instances": "must be a list of strings",
		"region":    "must be a string",
		"unknown":   "unknown field",
	}, errs)
}

func TestValidateNormalizesValuesAndAppliesDefaults(t *testing.T) {
	k := testKind()
	cfg := validConfig()
	cfg["base_url"] = " https://evolution.example.test "
	cfg["instances"] = []any{"main", " ", "backup"}
	cfg["extra_key"] = ""
	got, errs := k.Validate(cfg)
	require.Empty(t, errs)
	require.Equal(t, Config{
		"base_url":  "https://evolution.example.test",
		"api_key":   syntheticKey,
		"instances": []string{"main", "backup"},
		"verbose":   false,
		"region":    "eu",
	}, got, "trimmed, empty values dropped, defaults applied")
}

// CON-03: reads never contain a secret; set secrets read as the mask, unset ones are absent.
func TestMaskedHidesSecrets(t *testing.T) {
	k := testKind()
	cfg, errs := k.Validate(validConfig())
	require.Empty(t, errs)

	masked := k.Masked(cfg)
	require.Equal(t, dto.SecretMask, masked["api_key"])
	require.NotContains(t, masked, "extra_key", "unset secrets are not reported")
	require.Equal(t, "https://evolution.example.test", masked["base_url"])
	data, err := json.Marshal(masked)
	require.NoError(t, err)
	require.NotContains(t, string(data), syntheticKey)
}

// CON-03: a PATCH sending the mask keeps the stored secret; a new value replaces it.
func TestMergeKeepsSecretSentAsMask(t *testing.T) {
	k := testKind()
	stored, errs := k.Validate(validConfig())
	require.Empty(t, errs)

	merged := k.Merge(stored, Config{"api_key": dto.SecretMask, "region": "us"})
	got, errs := k.Validate(merged)
	require.Empty(t, errs)
	require.Equal(t, syntheticKey, got["api_key"])
	require.Equal(t, "us", got["region"])

	merged = k.Merge(stored, Config{"api_key": "sk-test-new"})
	require.Equal(t, "sk-test-new", merged["api_key"])

	merged = k.Merge(stored, Config{"instances": nil})
	require.NotContains(t, merged, "instances", "null clears a field")
	require.Equal(t, syntheticKey, stored["api_key"], "the stored config is not modified")
}

// SEC-04: secrets leave the public JSON and are sealed with the instance key.
func TestEncodeSealsSecretsAndDecodeRestoresThem(t *testing.T) {
	k := testKind()
	box := testBox(t)
	cfg, errs := k.Validate(validConfig())
	require.Empty(t, errs)

	public, sealed, err := k.Encode(box, cfg)
	require.NoError(t, err)
	require.NotContains(t, public, syntheticKey)
	require.NotContains(t, public, "api_key")
	require.NotEmpty(t, sealed)
	require.False(t, bytes.Contains(sealed, []byte(syntheticKey)), "sealed bytes do not hold the clear secret")

	back, err := Decode(box, public, sealed)
	require.NoError(t, err)
	require.Equal(t, syntheticKey, back["api_key"])
	require.Equal(t, "https://evolution.example.test", back["base_url"])
	require.Equal(t, []string{}, back.Strings("instances"), "absent lists read as empty")

	other, err := secrets.NewBox(bytes.Repeat([]byte{9}, secrets.KeySize))
	require.NoError(t, err)
	_, err = Decode(other, public, sealed)
	require.Error(t, err, "a wrong key cannot open the secrets")
}

func TestEncodeWithoutSecretsStoresNoSealedValue(t *testing.T) {
	k := Kind{Name: "plain", Fields: []Field{{Name: "region", Type: dto.FieldTypeString}}}
	public, sealed, err := k.Encode(testBox(t), Config{"region": "eu"})
	require.NoError(t, err)
	require.Nil(t, sealed)
	require.JSONEq(t, `{"region":"eu"}`, public)
}
