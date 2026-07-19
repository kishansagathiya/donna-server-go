package tools

import "testing"

func TestValidatePublicURL_rejectsLocalAndPrivate(t *testing.T) {
	cases := []string{
		"http://localhost/admin",
		"https://127.0.0.1/",
		"http://0.0.0.0:8080/",
		"http://192.168.1.10/path",
		"http://10.0.0.5/",
		"http://172.16.0.2/",
		"file:///etc/passwd",
		"ftp://example.com/a",
		"",
	}
	for _, raw := range cases {
		if _, err := ValidatePublicURL(raw); err == nil {
			t.Fatalf("expected rejection for %q", raw)
		}
	}
}

func TestValidatePublicURL_acceptsPublicIPLiteral(t *testing.T) {
	// 1.1.1.1 is public Cloudflare DNS
	got, err := ValidatePublicURL("https://1.1.1.1/")
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "1.1.1.1" {
		t.Fatalf("host = %q", got.Host)
	}
}
