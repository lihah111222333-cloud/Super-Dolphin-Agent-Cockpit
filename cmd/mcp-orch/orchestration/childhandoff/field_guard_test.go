package childhandoff

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestMaterializedJSONSurfaceExcludesSecretsAndPaths(t *testing.T) {
	typeOf := reflect.TypeFor[Materialized]()
	var exported []string
	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.PkgPath == "" {
			exported = append(exported, strings.Split(field.Tag.Get("json"), ",")[0])
		}
	}
	sort.Strings(exported)
	if got, want := strings.Join(exported, ","), "kind,provider"; got != want {
		t.Fatalf("Materialized JSON fields = %q, want %q", got, want)
	}
}

func TestProviderEnumIsClosed(t *testing.T) {
	if _, _, _, err := renderConfig(Provider("unknown"), "http://127.0.0.1:1", "ENV", t.TempDir()); err != ErrProviderDisabled {
		t.Fatalf("unknown provider error = %v, want ErrProviderDisabled", err)
	}
}
