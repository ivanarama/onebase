package launcher

import (
	"reflect"
	"testing"
)

func TestEnvironmentWithoutIsCaseInsensitiveAndRemovesDuplicates(t *testing.T) {
	env := []string{
		"PATH=/bin",
		"ONEBASE_WEBVIEW_PROFILE=first",
		"onebase_webview_profile=second",
		"ONEBASE_WEBVIEW_PROFILE_EXTRA=keep",
	}
	got := environmentWithout(env, "ONEBASE_WEBVIEW_PROFILE")
	want := []string{"PATH=/bin", "ONEBASE_WEBVIEW_PROFILE_EXTRA=keep"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("environmentWithout = %v, want %v", got, want)
	}
}
