package feeds

import "testing"

func TestPlainText(t *testing.T) {
	got := plainText(` <p>Hi &amp; <b>there</b></p><script>x()</script><style>a{}</style>  `, 100)
	if got != "Hi & there" {
		t.Errorf("got %q", got)
	}
	got = plainText("abcdefghij", 5)
	if got != "abcde…" {
		t.Errorf("cut: %q", got)
	}
}
