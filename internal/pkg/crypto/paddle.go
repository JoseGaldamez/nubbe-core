package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VerifyPaddleSignature valida que el webhook provenga genuinamente de Paddle.
// La cabecera Paddle-Signature tiene el formato: ts=TIMESTAMP;h1=SIGNATURE
func VerifyPaddleSignature(signatureHeader string, rawBody []byte, secret string) (bool, error) {
	if signatureHeader == "" || secret == "" {
		return false, fmt.Errorf("missing signature header or secret")
	}

	var tsStr, h1Hex string
	parts := strings.Split(signatureHeader, ";")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key == "ts" {
			tsStr = val
		} else if key == "h1" {
			h1Hex = val
		}
	}

	if tsStr == "" || h1Hex == "" {
		return false, fmt.Errorf("invalid signature header format")
	}

	// 1. Validar ventana de tiempo (prevenir Replay Attacks - límite de 5 minutos)
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("failed to parse signature timestamp: %w", err)
	}
	diff := time.Now().Unix() - ts
	if diff < -300 || diff > 300 {
		return false, fmt.Errorf("signature expired or timestamp is in the future")
	}

	// 2. Construir carga firmada: timestamp + ":" + rawBody
	signedPayload := fmt.Sprintf("%s:%s", tsStr, string(rawBody))

	// 3. Computar HMAC-SHA256
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	// 4. Comparación en tiempo constante para evitar ataques de canal lateral
	return subtle.ConstantTimeCompare([]byte(h1Hex), []byte(expectedMAC)) == 1, nil
}
