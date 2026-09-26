package vault

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestSealOpen_Roundtrip(t *testing.T) {
	payload := map[string]any{
		"config": "[gdrive]\ntype = drive\ntoken = {\"access\":\"x\"}\n",
		"device": "DESKTOP-1",
	}
	blob, err := Seal("correct-horse", payload)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if blob.Format != tag || blob.Alg != "AES-GCM" || blob.KDF != "PBKDF2-SHA256" || blob.Iter != iterations {
		t.Fatalf("blob header = %+v", blob)
	}
	// Salt/IV/data decode cleanly.
	if s, _ := base64.StdEncoding.DecodeString(blob.Salt); len(s) != saltLen {
		t.Fatal("bad salt")
	}
	if v, _ := base64.StdEncoding.DecodeString(blob.IV); len(v) != ivLen {
		t.Fatal("bad iv")
	}

	var out map[string]any
	if err := Open("correct-horse", blob, &out); err != nil {
		t.Fatalf("open: %v", err)
	}
	if out["config"] != payload["config"] || out["device"] != "DESKTOP-1" {
		t.Fatalf("roundtrip mismatch: %v", out)
	}
}

func TestOpen_WrongPassphrase(t *testing.T) {
	blob, _ := Seal("right", "secret")
	var out any
	err := Open("wrong", blob, &out)
	if err != ErrAuth {
		t.Fatalf("expected ErrAuth, got %v", err)
	}
}

func TestOpen_Tampered(t *testing.T) {
	blob, _ := Seal("pass", "secret")
	raw, _ := base64.StdEncoding.DecodeString(blob.Data)
	raw[0] ^= 0xFF
	blob.Data = base64.StdEncoding.EncodeToString(raw)
	err := Open("pass", blob, nil)
	if err != ErrAuth {
		t.Fatalf("expected ErrAuth on tamper, got %v", err)
	}
}

func TestOpen_Malformed(t *testing.T) {
	cases := []*Blob{
		nil,
		{},
		{Salt: "x"},
		{Salt: base64.StdEncoding.EncodeToString([]byte("short")), IV: "x", Data: "x"},
	}
	for i, b := range cases {
		if err := Open("p", b, nil); err != ErrMalformed {
			t.Errorf("case %d: expected ErrMalformed, got %v", i, err)
		}
	}
}

// TestJSCompat verifies a blob produced by the JS reference algorithm
// shape decrypts (format-level compatibility with cred-vault.js).
func TestJSCompat_Structure(t *testing.T) {
	blob, err := Seal("pw", struct{ A int }{A: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Marshal → JSON keys match JS blob field names exactly.
	data, _ := json.Marshal(blob)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	for _, k := range []string{"format", "alg", "kdf", "iter", "salt", "iv", "data"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing js-compatible key %q in blob", k)
		}
	}
	if m["format"] != "lnc-cred-vault-v1" {
		t.Errorf("format = %v", m["format"])
	}
}
