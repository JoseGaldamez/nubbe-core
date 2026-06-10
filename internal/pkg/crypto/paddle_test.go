package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestVerifyPaddleSignature(t *testing.T) {
	secret := "my_paddle_secret_key"
	body := []byte(`{"event_id":"evt_123","event_type":"subscription.created"}`)

	// Generar firma válida en tiempo real
	now := time.Now().Unix()
	payload := fmt.Sprintf("%d:%s", now, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	validSignature := hex.EncodeToString(mac.Sum(nil))
	validHeader := fmt.Sprintf("ts=%d;h1=%s", now, validSignature)

	// Generar firma expirada (10 minutos en el pasado)
	expiredTime := time.Now().Unix() - 600
	expiredPayload := fmt.Sprintf("%d:%s", expiredTime, string(body))
	macExpired := hmac.New(sha256.New, []byte(secret))
	macExpired.Write([]byte(expiredPayload))
	expiredSignature := hex.EncodeToString(macExpired.Sum(nil))
	expiredHeader := fmt.Sprintf("ts=%d;h1=%s", expiredTime, expiredSignature)

	tests := []struct {
		name      string
		header    string
		body      []byte
		secret    string
		wantOk    bool
		wantErr   bool
	}{
		{
			name:    "valid signature",
			header:  validHeader,
			body:    body,
			secret:  secret,
			wantOk:  true,
			wantErr: false,
		},
		{
			name:    "invalid secret",
			header:  validHeader,
			body:    body,
			secret:  "wrong_secret",
			wantOk:  false,
			wantErr: false, // Signature check fails, no error returned
		},
		{
			name:    "tampered body",
			header:  validHeader,
			body:    []byte(`{"event_id":"evt_123","event_type":"subscription.tampered"}`),
			secret:  secret,
			wantOk:  false,
			wantErr: false,
		},
		{
			name:    "expired timestamp",
			header:  expiredHeader,
			body:    body,
			secret:  secret,
			wantOk:  false,
			wantErr: true, // Should error out due to window validation (replay attack protection)
		},
		{
			name:    "empty header",
			header:  "",
			body:    body,
			secret:  secret,
			wantOk:  false,
			wantErr: true,
		},
		{
			name:    "malformed header",
			header:  "ts=abc;h1=123",
			body:    body,
			secret:  secret,
			wantOk:  false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := VerifyPaddleSignature(tt.header, tt.body, tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifyPaddleSignature() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantOk {
				t.Errorf("VerifyPaddleSignature() = %v, wantOk %v", got, tt.wantOk)
			}
		})
	}
}

func TestVerifyPaddleSignatureHeaderParsing(t *testing.T) {
	// Probar que el parsing de cabeceras es tolerante a espacios y desorden
	secret := "secret"
	body := []byte("hello")
	now := time.Now().Unix()
	payload := fmt.Sprintf("%d:hello", now)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	header := fmt.Sprintf(" h1 = %s ; ts = %s ", signature, strconv.FormatInt(now, 10))
	ok, err := VerifyPaddleSignature(header, body, secret)
	if err != nil {
		t.Fatalf("unexpected error parsing spaced header: %v", err)
	}
	if !ok {
		t.Error("expected signature to be verified successfully even with spacing and reordered fields")
	}
}
