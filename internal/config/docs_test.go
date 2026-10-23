package config

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// configKeys lists every YAML key path in Config ("dns.cache.min_ttl"),
// following structs, pointers and slices of structs.
func configKeys() []string {
	var out []string
	var walk func(t reflect.Type, prefix string)
	walk = func(t reflect.Type, prefix string) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct || t.PkgPath() == "time" {
			return
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("yaml"), ",")[0]
			if !f.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			out = append(out, prefix+name)
			walk(f.Type, prefix+name+".")
		}
	}
	walk(reflect.TypeOf(Config{}), "")
	return out
}

// The in-app Help (web/src/lib/docs.ts, also generated into
// docs/user-guide.md) documents every setting. Adding a config key without
// documenting it — or leaving a removed key documented — fails here.
func TestEverySettingIsDocumented(t *testing.T) {
	src, err := os.ReadFile("../../web/src/lib/docs.ts")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`key: '([a-z0-9_.]+)'`).FindAllStringSubmatch(text, -1) {
		documented[m[1]] = true
	}
	structural := map[string]bool{}
	if m := regexp.MustCompile(`structuralKeys = \[([^\]]*)\]`).FindStringSubmatch(text); m != nil {
		for _, k := range regexp.MustCompile(`'([a-z0-9_.]+)'`).FindAllStringSubmatch(m[1], -1) {
			structural[k[1]] = true
		}
	}
	keys := configKeys()
	known := map[string]bool{}
	var missing []string
	for _, k := range keys {
		known[k] = true
		if !documented[k] && !structural[k] {
			missing = append(missing, k)
		}
	}
	var stale []string
	for k := range documented {
		if !known[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("config keys with no entry in web/src/lib/docs.ts:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("docs.ts documents keys that no longer exist:\n  %s", strings.Join(stale, "\n  "))
	}
}
