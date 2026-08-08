package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// sessionRow espelha as colunas persistidas de cada canal. Os campos de proxy
// são preenchidos mesmo em sessões legadas (ficam zerados via COALESCE), de forma
// que migrateSessionColumns pode rodar de forma idempotente em bases em produção.
type sessionRow struct {
	ID   string
	Name string
	JID  string

	ProxyEnabled                          bool
	ProxyType, ProxyHost, ProxyUsername,
	ProxyPassword                        string
	ProxyPort                            int
}

type sessionStore struct{ db *sql.DB }

func newSessionStore(ctx context.Context, db *sql.DB) (*sessionStore, error) {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sessions (
		id   TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		jid  TEXT
	)`)
	if err != nil {
		return nil, err
	}
	if err := migrateSessionColumns(ctx, db); err != nil {
		return nil, fmt.Errorf("migrate sessions proxy columns: %w", err)
	}
	return &sessionStore{db: db}, nil
}

// migrateSessionColumns adiciona as colunas de proxy à tabela sessions de forma
// idempotente. Em produção a tabela já existe (criada por versões anteriores sem
// suporte a proxy), então CREATE TABLE IF NOT EXISTS não acrescenta colunas — é
// preciso ALTER TABLE ... ADD COLUMN para cada uma, pulando as já presentes.
func migrateSessionColumns(ctx context.Context, db *sql.DB) error {
	cols, err := tableColumns(db, "sessions")
	if err != nil {
		return err
	}
	// Ordem: (nome da coluna, cláusula ADD COLUMN completa).
	additions := []struct{ name, ddl string }{
		{"proxy_enabled", "proxy_enabled INTEGER NOT NULL DEFAULT 0"},
		{"proxy_type", "proxy_type TEXT"},
		{"proxy_host", "proxy_host TEXT"},
		{"proxy_port", "proxy_port INTEGER"},
		{"proxy_username", "proxy_username TEXT"},
		{"proxy_password", "proxy_password TEXT"},
	}
	for _, add := range additions {
		if contains(cols, add.name) {
			continue
		}
		if _, err := db.ExecContext(ctx, "ALTER TABLE sessions ADD COLUMN "+add.ddl); err != nil {
			return fmt.Errorf("add column %s: %w", add.name, err)
		}
	}
	return nil
}

// tableColumns lista os nomes das colunas de uma tabela via PRAGMA table_info.
// Generaliza deviceColumns (session_store.go) para reaproveitamento em migrations.
func tableColumns(db *sql.DB, table string) ([]string, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
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

// DB returns the underlying database handle, shared with whatsmeow's own tables.
func (s *sessionStore) DB() *sql.DB {
	return s.db
}

func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *sessionStore) list(ctx context.Context) ([]sessionRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, COALESCE(jid, ''),
		COALESCE(proxy_enabled, 0), COALESCE(proxy_type, ''), COALESCE(proxy_host, ''),
		COALESCE(proxy_port, 0), COALESCE(proxy_username, ''), COALESCE(proxy_password, '')
		FROM sessions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.ID, &r.Name, &r.JID,
			&r.ProxyEnabled, &r.ProxyType, &r.ProxyHost,
			&r.ProxyPort, &r.ProxyUsername, &r.ProxyPassword); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// getProxy lê a configuração de proxy persistida de um canal. Sessões sem proxy
// (ou inexistentes) devolvem ProxyConfig zerada com Enabled=false e erro nil.
func (s *sessionStore) getProxy(ctx context.Context, id string) (ProxyConfig, error) {
	var (
		cfg  ProxyConfig
		port int
		enab int
	)
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(proxy_enabled, 0), COALESCE(proxy_type, ''), COALESCE(proxy_host, ''),
		COALESCE(proxy_port, 0), COALESCE(proxy_username, ''), COALESCE(proxy_password, '')
		FROM sessions WHERE id = ?`, id).Scan(&enab, &cfg.Type, &cfg.Host, &port, &cfg.Username, &cfg.Password)
	if err != nil {
		if err == sql.ErrNoRows {
			return ProxyConfig{}, nil
		}
		return ProxyConfig{}, err
	}
	cfg.Enabled = enab != 0
	cfg.Port = port
	return cfg, nil
}

// setProxy persiste a configuração de proxy de um canal. Não cria a sessão: o
// UPDATE silencioso é intencional, pois proxy só é configurado em canais já salvos.
func (s *sessionStore) setProxy(ctx context.Context, id string, cfg ProxyConfig) error {
	enab := 0
	if cfg.Enabled {
		enab = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET
		proxy_enabled = ?, proxy_type = ?, proxy_host = ?,
		proxy_port = ?, proxy_username = ?, proxy_password = ? WHERE id = ?`,
		enab, cfg.Type, cfg.Host, cfg.Port, cfg.Username, cfg.Password, id)
	return err
}

func (s *sessionStore) insert(ctx context.Context, id, name string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, name, jid) VALUES (?, ?, NULL)`, id, name)
	return err
}

func (s *sessionStore) setJID(ctx context.Context, id, jid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET jid = ? WHERE id = ?`, jid, id)
	return err
}

func (s *sessionStore) delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}
