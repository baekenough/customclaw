-- NextAuth.js User and Account tables
-- Migration: 002_nextauth.sql

CREATE TABLE IF NOT EXISTS users (
  id             VARCHAR(36)  PRIMARY KEY,
  name           VARCHAR(255),
  email          VARCHAR(255) UNIQUE,
  email_verified TIMESTAMPTZ,
  image          TEXT
);

CREATE TABLE IF NOT EXISTS accounts (
  id                  VARCHAR(36)  PRIMARY KEY,
  user_id             VARCHAR(36)  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type                VARCHAR(64)  NOT NULL,
  provider            VARCHAR(64)  NOT NULL,
  provider_account_id VARCHAR(255) NOT NULL,
  refresh_token       TEXT,
  access_token        TEXT,
  expires_at          INTEGER,
  token_type          VARCHAR(64),
  scope               TEXT,
  id_token            TEXT,
  session_state       TEXT,
  UNIQUE (provider, provider_account_id)
);
