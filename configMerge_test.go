package main

// Tests for the config overlay: user_config.json layered on top of the
// embedded default config. The array-replace-vs-append rule here is the
// difference between a device showing its intended layout and showing every
// element twice, so it is pinned in detail.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// mustJSONMap parses a JSON object literal into the generic map shape that
// deepMergeJSON operates on.
func mustJSONMap(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test JSON %q: %v", s, err)
	}
	return m
}

func TestDeepMergeJSONPrimitivesAndNesting(t *testing.T) {
	dst := mustJSONMap(t, `{
		"a": 1,
		"b": "default",
		"nested": {"keep": "yes", "override": "old"},
		"only_in_dst": true
	}`)
	src := mustJSONMap(t, `{
		"b": "user",
		"nested": {"override": "new", "added": 42},
		"only_in_src": "hello"
	}`)

	got := deepMergeJSON(dst, src)

	if got["a"] != float64(1) {
		t.Errorf("untouched key a = %v, want 1", got["a"])
	}
	if got["b"] != "user" {
		t.Errorf("src should override primitive: b = %v, want \"user\"", got["b"])
	}
	if got["only_in_dst"] != true {
		t.Errorf("dst-only key was dropped: %v", got["only_in_dst"])
	}
	if got["only_in_src"] != "hello" {
		t.Errorf("src-only key was not copied: %v", got["only_in_src"])
	}

	nested, ok := got["nested"].(map[string]interface{})
	if !ok {
		t.Fatalf("nested is %T, want map", got["nested"])
	}
	if nested["keep"] != "yes" {
		t.Errorf("nested dst-only key lost: %v", nested["keep"])
	}
	if nested["override"] != "new" {
		t.Errorf("nested override = %v, want \"new\"", nested["override"])
	}
	if nested["added"] != float64(42) {
		t.Errorf("nested src-only key lost: %v", nested["added"])
	}
}

// A nil in src means "not specified", not "set to null" — the default must
// survive. Otherwise a sparse user_config wipes shipped values.
func TestDeepMergeJSONNilSrcValuePreservesDefault(t *testing.T) {
	dst := mustJSONMap(t, `{"ping_site_0": "8.8.8.8"}`)
	src := mustJSONMap(t, `{"ping_site_0": null}`)

	got := deepMergeJSON(dst, src)
	if got["ping_site_0"] != "8.8.8.8" {
		t.Errorf("null in src overwrote default: %v", got["ping_site_0"])
	}
}

// Outside display_template, arrays append. This is what lets a user_config add
// extra custom entries to a shipped list.
func TestDeepMergeJSONArraysAppendByDefault(t *testing.T) {
	dst := mustJSONMap(t, `{"list": [1, 2]}`)
	src := mustJSONMap(t, `{"list": [3]}`)

	got := deepMergeJSON(dst, src)
	list, ok := got["list"].([]interface{})
	if !ok {
		t.Fatalf("list is %T, want slice", got["list"])
	}
	want := []interface{}{float64(1), float64(2), float64(3)}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("got %v, want %v (arrays append outside display_template)", list, want)
	}
}

// The regression this rule exists for: a user_config carrying a complete
// display_template.elements layout must REPLACE the shipped page arrays. If it
// appended, every element on the page would be drawn twice.
func TestDeepMergeJSONDisplayTemplateArraysReplace(t *testing.T) {
	dst := mustJSONMap(t, `{
		"display_template": {
			"elements": {
				"page0": [{"data_key": "shipped_a"}, {"data_key": "shipped_b"}]
			}
		}
	}`)
	src := mustJSONMap(t, `{
		"display_template": {
			"elements": {
				"page0": [{"data_key": "user_only"}]
			}
		}
	}`)

	got := deepMergeJSON(dst, src)

	tmpl := got["display_template"].(map[string]interface{})
	els := tmpl["elements"].(map[string]interface{})
	page0, ok := els["page0"].([]interface{})
	if !ok {
		t.Fatalf("page0 is %T, want slice", els["page0"])
	}
	if len(page0) != 1 {
		t.Fatalf("page0 has %d elements, want 1 — user layout must replace, "+
			"not append to, the shipped layout", len(page0))
	}
	first := page0[0].(map[string]interface{})
	if first["data_key"] != "user_only" {
		t.Errorf("page0[0].data_key = %v, want \"user_only\"", first["data_key"])
	}
}

// The replace rule must reach arbitrarily deep below the subtree root, since
// the layout arrays sit two levels down (display_template.elements.pageN).
func TestDeepMergeJSONReplaceAppliesToWholeSubtree(t *testing.T) {
	for _, root := range []string{"display_template", "custom_metrics", "public_ip_lookup"} {
		t.Run(root, func(t *testing.T) {
			dst := mustJSONMap(t, `{"`+root+`": {"deep": {"deeper": {"arr": [1, 2, 3]}}}}`)
			src := mustJSONMap(t, `{"`+root+`": {"deep": {"deeper": {"arr": [9]}}}}`)

			got := deepMergeJSON(dst, src)
			arr := got[root].(map[string]interface{})["deep"].(map[string]interface{})["deeper"].(map[string]interface{})["arr"].([]interface{})
			if len(arr) != 1 || arr[0] != float64(9) {
				t.Errorf("under %s got %v, want [9] — replace must apply at any depth", root, arr)
			}
		})
	}
}

// Zero is treated as "unset" for these three fields only. A zero brightness
// ceiling or dimmer timeout would black the screen out permanently, so a
// malformed user_config must not be able to set it.
func TestDeepMergeJSONZeroGuardedFields(t *testing.T) {
	guarded := []string{
		"screen_max_brightness",
		"screen_dimmer_time_on_battery_seconds",
		"screen_dimmer_time_on_dc_seconds",
	}
	for _, key := range guarded {
		t.Run(key+"/zero_ignored", func(t *testing.T) {
			dst := mustJSONMap(t, `{"`+key+`": 100}`)
			src := mustJSONMap(t, `{"`+key+`": 0}`)
			got := deepMergeJSON(dst, src)
			if got[key] != float64(100) {
				t.Errorf("zero from src overrode default: %v, want 100", got[key])
			}
		})
		t.Run(key+"/nonzero_applied", func(t *testing.T) {
			dst := mustJSONMap(t, `{"`+key+`": 100}`)
			src := mustJSONMap(t, `{"`+key+`": 55}`)
			got := deepMergeJSON(dst, src)
			if got[key] != float64(55) {
				t.Errorf("non-zero value not applied: %v, want 55", got[key])
			}
		})
	}
}

// screen_min_brightness is deliberately NOT zero-guarded: 0 means "backlight
// fully off when idle", a legitimate user choice.
func TestDeepMergeJSONMinBrightnessZeroIsHonoured(t *testing.T) {
	dst := mustJSONMap(t, `{"screen_min_brightness": 10}`)
	src := mustJSONMap(t, `{"screen_min_brightness": 0}`)

	got := deepMergeJSON(dst, src)
	if got["screen_min_brightness"] != float64(0) {
		t.Errorf("screen_min_brightness = %v, want 0 — zero is a valid setting here",
			got["screen_min_brightness"])
	}
}

// Type mismatches resolve to src rather than panicking or silently keeping dst.
func TestDeepMergeJSONTypeMismatchSrcWins(t *testing.T) {
	cases := []struct {
		name     string
		dst, src string
		want     interface{}
	}{
		{"array_over_scalar", `{"k": 5}`, `{"k": [1]}`, []interface{}{float64(1)}},
		{"scalar_over_array", `{"k": [1]}`, `{"k": 5}`, float64(5)},
		{"scalar_over_object", `{"k": {"a": 1}}`, `{"k": "str"}`, "str"},
		{"object_over_scalar", `{"k": 7}`, `{"k": {"a": 1}}`, map[string]interface{}{"a": float64(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deepMergeJSON(mustJSONMap(t, tc.dst), mustJSONMap(t, tc.src))
			if !reflect.DeepEqual(got["k"], tc.want) {
				t.Errorf("got %#v, want %#v", got["k"], tc.want)
			}
		})
	}
}

func TestDeepMergeJSONEmptySrcIsIdentity(t *testing.T) {
	dst := mustJSONMap(t, `{"a": 1, "b": {"c": [1,2]}}`)
	want := mustJSONMap(t, `{"a": 1, "b": {"c": [1,2]}}`)

	got := deepMergeJSON(dst, map[string]interface{}{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("empty src changed dst:\ngot  %#v\nwant %#v", got, want)
	}
}

// mergeConfigs is the real entry point: it round-trips both structs through
// JSON, merges, and unmarshals back into cfg.
//
// Note the structural limit this exposes: userCfg is a Config struct, and the
// string fields have no omitempty, so a field the user never set marshals as
// "" and *does* overwrite the default. Only the explicitly zero-guarded
// numeric keys and the post-merge ping-site fallback protect against that.
func TestMergeConfigsOverlaysUserOntoDefault(t *testing.T) {
	savedDft, savedUser, savedCfg := dftCfg, userCfg, cfg
	defer func() { dftCfg, userCfg, cfg = savedDft, savedUser, savedCfg }()

	dftCfg = Config{
		ScreenMaxBrightness:              100,
		ScreenMinBrightness:              10,
		ScreenDimmerTimeOnBatterySeconds: 30,
		ScreenDimmerTimeOnDCSeconds:      300,
		PingSite0:                        "8.8.8.8",
		PingSite1:                        "1.1.1.1",
		DisplayTemplate: DisplayTemplate{
			Elements: map[string][]DisplayElement{
				"page0": {{Type: "text", DataKey: "shipped"}},
			},
		},
	}
	userCfg = Config{
		PingSite0: "9.9.9.9",
		DisplayTemplate: DisplayTemplate{
			Elements: map[string][]DisplayElement{
				"page0": {{Type: "text", DataKey: "user"}},
			},
		},
	}

	if err := mergeConfigs(); err != nil {
		t.Fatalf("mergeConfigs: %v", err)
	}

	if cfg.PingSite0 != "9.9.9.9" {
		t.Errorf("user override lost: PingSite0 = %q", cfg.PingSite0)
	}
	// The user struct's zero-value PingSite1 wipes the default, then the
	// post-merge fallback fills the built-in. The slot must never end up blank.
	if cfg.PingSite1 != "photonicat.com" {
		t.Errorf("PingSite1 = %q, want the post-merge fallback \"photonicat.com\"",
			cfg.PingSite1)
	}
	// Numeric screen settings ARE zero-guarded, so a silent user struct keeps
	// the shipped values — these are the ones that would black out the panel.
	if cfg.ScreenMaxBrightness != 100 {
		t.Errorf("zero-guard failed: ScreenMaxBrightness = %d, want 100", cfg.ScreenMaxBrightness)
	}
	if cfg.ScreenDimmerTimeOnBatterySeconds != 30 {
		t.Errorf("zero-guard failed: dimmer-on-battery = %d, want 30",
			cfg.ScreenDimmerTimeOnBatterySeconds)
	}
	if cfg.ScreenDimmerTimeOnDCSeconds != 300 {
		t.Errorf("zero-guard failed: dimmer-on-dc = %d, want 300",
			cfg.ScreenDimmerTimeOnDCSeconds)
	}
	page0 := cfg.DisplayTemplate.Elements["page0"]
	if len(page0) != 1 || page0[0].DataKey != "user" {
		t.Errorf("layout should be replaced by the user copy, got %+v", page0)
	}
}

// A too-short ping site (or none at all) must resolve to a working default —
// the ping rows are drawn every frame and a blank host silently kills them.
func TestMergeConfigsPingSiteFallbacks(t *testing.T) {
	savedDft, savedUser, savedCfg := dftCfg, userCfg, cfg
	defer func() { dftCfg, userCfg, cfg = savedDft, savedUser, savedCfg }()

	dftCfg = Config{ScreenMaxBrightness: 100}
	userCfg = Config{PingSite0: "ab", PingSite1: ""} // "ab" is len 2, too short

	if err := mergeConfigs(); err != nil {
		t.Fatalf("mergeConfigs: %v", err)
	}
	if cfg.PingSite0 != "1.1.1.1" {
		t.Errorf("PingSite0 = %q, want \"1.1.1.1\"", cfg.PingSite0)
	}
	if cfg.PingSite1 != "photonicat.com" {
		t.Errorf("PingSite1 = %q, want \"photonicat.com\"", cfg.PingSite1)
	}
}

// Negative dimmer timeouts are rejected rather than silently accepted — a
// negative value would make the idle comparison always true.
func TestMergeConfigsRejectsNegativeDimmerTimes(t *testing.T) {
	savedDft, savedUser, savedCfg := dftCfg, userCfg, cfg
	defer func() { dftCfg, userCfg, cfg = savedDft, savedUser, savedCfg }()

	for _, tc := range []struct {
		name string
		user Config
	}{
		{"battery", Config{ScreenDimmerTimeOnBatterySeconds: -1}},
		{"dc", Config{ScreenDimmerTimeOnDCSeconds: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dftCfg = Config{ScreenMaxBrightness: 100, PingSite0: "1.1.1.1", PingSite1: "photonicat.com"}
			userCfg = tc.user
			if err := mergeConfigs(); err == nil {
				t.Error("mergeConfigs accepted a negative dimmer timeout")
			}
		})
	}
}
