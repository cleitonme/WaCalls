package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// writeSessionToWhatsmeow writes imported session credentials directly into
// whatsmeow's SQLite tables, replacing any existing device for the given JID.
// jid must be the canonical form (types.JID.String()) so the row matches what
// sqlstore.Container.GetDevice looks up afterwards.
func writeSessionToWhatsmeow(db *sql.DB, jid string, creds *SessionCredentials, log *slog.Logger) error {
	if len(creds.NoiseKeyPriv) == 0 {
		return fmt.Errorf("noise key private is empty – cannot restore session")
	}
	if jid == "" {
		return fmt.Errorf("JID is empty – cannot determine which account to restore")
	}

	if err := deleteExistingDevice(db, jid); err != nil {
		return fmt.Errorf("deleting existing device: %w", err)
	}

	if err := insertDevice(db, jid, creds); err != nil {
		return fmt.Errorf("inserting device: %w", err)
	}

	if err := insertPreKeys(db, jid, creds.PreKeys); err != nil {
		log.Warn("failed to insert pre-keys (non-fatal)", "err", err)
	}
	if err := insertAppStateSyncKeys(db, jid, creds.AppStateSyncKeys); err != nil {
		log.Warn("failed to insert app state sync keys (non-fatal)", "err", err)
	}
	if err := insertAppStateVersions(db, jid, creds.AppStateVersions); err != nil {
		log.Warn("failed to insert app state versions (non-fatal)", "err", err)
	}
	if err := insertIdentityKeys(db, jid, creds.IdentityKeys); err != nil {
		log.Warn("failed to insert identity keys (non-fatal)", "err", err)
	}

	log.Info("session written to whatsmeow database", "jid", jid)
	return nil
}

func deleteExistingDevice(db *sql.DB, jid string) error {
	if _, err := db.Exec(`DELETE FROM whatsmeow_privacy_tokens WHERE our_jid = ?`, jid); err != nil {
		return err
	}
	// Child tables cascade from whatsmeow_device via ON DELETE CASCADE.
	_, err := db.Exec(`DELETE FROM whatsmeow_device WHERE jid = ?`, jid)
	return err
}

// deviceColumns returns the actual column list for whatsmeow_device by probing the DB,
// since different whatsmeow versions have different columns (e.g. some lack "lid").
func deviceColumns(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info(whatsmeow_device)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt interface{}
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err == nil {
			cols = append(cols, name)
		}
	}
	return cols, rows.Err()
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// padBytes returns b padded/truncated to exactly n bytes.
func padBytes(b []byte, n int) []byte {
	if len(b) == n {
		return b
	}
	out := make([]byte, n)
	copy(out, b)
	return out
}

func insertDevice(db *sql.DB, jid string, creds *SessionCredentials) error {
	// Enforce byte-length constraints from schema CHECK clauses.
	advKey := padBytes(creds.AdvKey, 32)
	advAccountSig := padBytes(creds.AdvAccountSig, 64)
	advAccountSigKey := padBytes(creds.AdvAccountSigKey, 32)
	advDeviceSig := padBytes(creds.AdvDeviceSig, 64)
	advDetails := creds.AdvDetails
	if advDetails == nil {
		advDetails = []byte{}
	}

	platform := creds.Platform
	if platform == "" {
		platform = "web"
	}

	existingCols, err := deviceColumns(db)
	if err != nil {
		existingCols = []string{}
	}

	colNames := []string{
		"jid", "registration_id",
		"noise_key", "identity_key",
		"signed_pre_key", "signed_pre_key_id", "signed_pre_key_sig",
		"adv_key", "adv_details", "adv_account_sig", "adv_account_sig_key", "adv_device_sig",
		"platform", "push_name",
	}
	args := []interface{}{
		jid, int64(creds.RegistrationID),
		creds.NoiseKeyPriv, creds.IdentKeyPriv,
		creds.SignedPreKeyPriv, int(creds.SignedPreKeyID), creds.SignedPreKeySig,
		advKey, advDetails, advAccountSig, advAccountSigKey, advDeviceSig,
		platform, creds.PushName,
	}

	if len(existingCols) == 0 || contains(existingCols, "business_name") {
		colNames = append(colNames, "business_name")
		args = append(args, "")
	}
	if len(existingCols) == 0 || contains(existingCols, "lid") {
		colNames = append(colNames, "lid")
		args = append(args, creds.LID)
	}
	if len(existingCols) == 0 || contains(existingCols, "initialized") {
		colNames = append(colNames, "initialized")
		args = append(args, true)
	}

	colList := strings.Join(colNames, ", ")
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")

	q := fmt.Sprintf(`INSERT OR REPLACE INTO whatsmeow_device (%s) VALUES (%s)`, colList, placeholders)
	_, err = db.Exec(q, args...)
	return err
}

func insertPreKeys(db *sql.DB, jid string, preKeys map[uint32][]byte) error {
	for keyID, privKey := range preKeys {
		if len(privKey) == 0 {
			continue
		}
		q := `INSERT OR REPLACE INTO whatsmeow_pre_keys (jid, key_id, key, uploaded) VALUES (?, ?, ?, ?)`
		if _, err := db.Exec(q, jid, int(keyID), privKey, false); err != nil {
			return fmt.Errorf("pre-key %d: %w", keyID, err)
		}
	}
	return nil
}

func insertAppStateSyncKeys(db *sql.DB, jid string, keys []AppStateSyncKeyEntry) error {
	for _, k := range keys {
		if len(k.KeyID) == 0 || len(k.KeyData) == 0 {
			continue
		}
		q := `INSERT OR REPLACE INTO whatsmeow_app_state_sync_keys (jid, key_id, key_data, timestamp, fingerprint) VALUES (?, ?, ?, ?, ?)`
		if _, err := db.Exec(q, jid, k.KeyID, k.KeyData, k.Timestamp, []byte{}); err != nil {
			return fmt.Errorf("app state sync key: %w", err)
		}
	}
	return nil
}

func insertAppStateVersions(db *sql.DB, jid string, versions []AppStateVersionEntry) error {
	// Schema: whatsmeow_app_state_version (jid, name, version, hash) — hash must be 128 bytes.
	for _, v := range versions {
		if v.Collection == "" {
			continue
		}
		hash := padBytes(v.Hash, 128)
		q := `INSERT OR REPLACE INTO whatsmeow_app_state_version (jid, name, version, hash) VALUES (?, ?, ?, ?)`
		if _, err := db.Exec(q, jid, v.Collection, int64(v.Version), hash); err != nil {
			return fmt.Errorf("app state version %s: %w", v.Collection, err)
		}
	}
	return nil
}

func insertIdentityKeys(db *sql.DB, jid string, identityKeys map[string][]byte) error {
	for addr, key := range identityKeys {
		if len(key) == 0 {
			continue
		}
		// Schema uses column name "identity" not "key".
		q := `INSERT OR REPLACE INTO whatsmeow_identity_keys (our_jid, their_id, identity) VALUES (?, ?, ?)`
		if _, err := db.Exec(q, jid, addr, key); err != nil {
			return fmt.Errorf("identity key %s: %w", addr, err)
		}
	}
	return nil
}

// deviceCredsRow mirrors the columns of whatsmeow_device needed to rebuild a Baileys export.
type deviceCredsRow struct {
	RegistrationID   int64
	NoiseKey         []byte
	IdentityKey      []byte
	SignedPreKey     []byte
	SignedPreKeyID   int
	SignedPreKeySig  []byte
	AdvKey           []byte
	AdvDetails       []byte
	AdvAccountSig    []byte
	AdvAccountSigKey []byte
	AdvDeviceSig     []byte
	Platform         string
	PushName         string
	LID              string
}

// readDeviceCreds reads the device row for jid from whatsmeow's SQLite tables.
func readDeviceCreds(db *sql.DB, jid string) (*deviceCredsRow, error) {
	var row deviceCredsRow
	q := `SELECT registration_id, noise_key, identity_key, signed_pre_key,
		signed_pre_key_id, signed_pre_key_sig, adv_key, adv_details,
		adv_account_sig, adv_account_sig_key, adv_device_sig, platform, push_name, COALESCE(lid, '')
		FROM whatsmeow_device WHERE jid = ?`
	err := db.QueryRow(q, jid).Scan(
		&row.RegistrationID, &row.NoiseKey, &row.IdentityKey, &row.SignedPreKey,
		&row.SignedPreKeyID, &row.SignedPreKeySig, &row.AdvKey, &row.AdvDetails,
		&row.AdvAccountSig, &row.AdvAccountSigKey, &row.AdvDeviceSig, &row.Platform, &row.PushName, &row.LID,
	)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}
	return &row, nil
}

// buildBaileysExport constructs the Baileys creds.json structure from raw whatsmeow DB values.
func buildBaileysExport(jid string, row *deviceCredsRow) map[string]interface{} {
	toBufferJSON := func(b []byte) map[string]interface{} {
		if len(b) == 0 {
			return map[string]interface{}{"type": "Buffer", "data": []byte{}}
		}
		data := make([]int, len(b))
		for i, v := range b {
			data[i] = int(v)
		}
		return map[string]interface{}{"type": "Buffer", "data": data}
	}

	return map[string]interface{}{
		"noiseKey": map[string]interface{}{
			"private": toBufferJSON(row.NoiseKey),
			"public":  toBufferJSON(nil), // public not stored; derive if needed
		},
		"signedIdentityKey": map[string]interface{}{
			"private": toBufferJSON(row.IdentityKey),
			"public":  toBufferJSON(nil),
		},
		"signedPreKey": map[string]interface{}{
			"keyPair": map[string]interface{}{
				"private": toBufferJSON(row.SignedPreKey),
				"public":  toBufferJSON(nil),
			},
			"signature": toBufferJSON(row.SignedPreKeySig),
			"keyId":     row.SignedPreKeyID,
		},
		"registrationId": row.RegistrationID,
		"advSecretKey":   row.AdvKey,
		"me": map[string]interface{}{
			"id":  jid,
			"lid": row.LID,
		},
		"account": map[string]interface{}{
			"details":             toBufferJSON(row.AdvDetails),
			"accountSignature":    toBufferJSON(row.AdvAccountSig),
			"accountSignatureKey": toBufferJSON(row.AdvAccountSigKey),
			"deviceSignature":     toBufferJSON(row.AdvDeviceSig),
		},
		"platform": row.Platform,
	}
}

// validateCredentials checks that the minimum required fields are present.
func validateCredentials(creds *SessionCredentials) error {
	if len(creds.NoiseKeyPriv) != 32 {
		return fmt.Errorf("noiseKey.private must be 32 bytes, got %d", len(creds.NoiseKeyPriv))
	}
	if len(creds.IdentKeyPriv) != 32 {
		return fmt.Errorf("identityKey.private must be 32 bytes, got %d", len(creds.IdentKeyPriv))
	}
	if len(creds.SignedPreKeyPriv) != 32 {
		return fmt.Errorf("signedPreKey.private must be 32 bytes, got %d", len(creds.SignedPreKeyPriv))
	}
	if len(creds.SignedPreKeySig) != 64 {
		return fmt.Errorf("signedPreKey.signature must be 64 bytes, got %d", len(creds.SignedPreKeySig))
	}
	if creds.RegistrationID == 0 {
		return fmt.Errorf("registrationId is 0 or missing")
	}
	if strings.TrimSpace(creds.JID) == "" {
		return fmt.Errorf("jid is missing")
	}
	return nil
}
