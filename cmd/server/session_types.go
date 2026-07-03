package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// BufferJSON is the { "type": "Buffer", "data": "<base64>" } format used in JS dumps.
// Fields can also be raw base64 strings for some Baileys fields.
type BufferJSON struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// Bytes decodes the BufferJSON to raw bytes.
func (b *BufferJSON) Bytes() ([]byte, error) {
	if b == nil || b.Data == "" {
		return nil, nil
	}
	dec, err := base64.StdEncoding.DecodeString(b.Data)
	if err != nil {
		dec, err = base64.RawStdEncoding.DecodeString(b.Data)
	}
	return dec, err
}

// flexBytes decodes a JSON value that is either a BufferJSON object or a plain base64 string.
func flexBytes(raw json.RawMessage, field string) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var buf BufferJSON
	if err := json.Unmarshal(raw, &buf); err == nil && buf.Type == "Buffer" {
		return buf.Bytes()
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		dec, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			dec, err = base64.RawStdEncoding.DecodeString(s)
		}
		if err == nil {
			return dec, nil
		}
	}
	return nil, fmt.Errorf("%s: cannot decode as BufferJSON or base64 string", field)
}

// SessionCredentials holds the cryptographic material needed to restore a WhatsApp session
// imported from an external dump (wa-web or Baileys format).
type SessionCredentials struct {
	JID            string
	LID            string
	RegistrationID uint32
	Platform       string
	PushName       string

	NoiseKeyPriv []byte
	NoiseKeyPub  []byte
	IdentKeyPriv []byte
	IdentKeyPub  []byte

	SignedPreKeyPriv []byte
	SignedPreKeyPub  []byte
	SignedPreKeySig  []byte
	SignedPreKeyID   uint32

	AdvKey           []byte
	AdvDetails       []byte
	AdvAccountSig    []byte
	AdvAccountSigKey []byte
	AdvDeviceSig     []byte

	PreKeys map[uint32][]byte

	AppStateSyncKeys []AppStateSyncKeyEntry
	AppStateVersions []AppStateVersionEntry

	Sessions     map[string][]byte
	SenderKeys   map[string][]byte
	IdentityKeys map[string][]byte
}

type AppStateSyncKeyEntry struct {
	KeyID     []byte
	KeyData   []byte
	Timestamp int64
}

type AppStateVersionEntry struct {
	Collection    string
	Version       uint64
	Hash          []byte
	IndexValueMap map[string][]byte
}

// importFormat identifies the source format of a session dump.
type importFormat string

const (
	formatWaWeb   importFormat = "wa-web"
	formatBaileys importFormat = "baileys"
	formatUnknown importFormat = "unknown"
)

// importStatus tracks the current status of an import operation for a session.
type importStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	JID     string `json:"jid,omitempty"`
}
