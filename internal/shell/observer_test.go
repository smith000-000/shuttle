package shell

import "testing"

func TestSanitizeCapturedBody(t *testing.T) {
	body := "prompt% echo __SHUTTLE_B__:cmd-1\nprompt% printf 'alpha\\n'; false\nalpha\nprompt% echo __SHUTTLE_E__:cmd-1:1\nabc123:$?"

	got := sanitizeCapturedBody(body)
	want := "prompt% printf 'alpha\\n'; false\nalpha"

	if got != want {
		t.Fatalf("sanitizeCapturedBody() = %q, want %q", got, want)
	}
}
