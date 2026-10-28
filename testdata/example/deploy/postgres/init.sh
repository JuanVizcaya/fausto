#!/bin/sh
# Creates the databases on the first start of an empty volume: habbyte (one
# schema per module), habbyte_test for the integration tests, and keycloak.
# Per-module owner and runtime roles are created by the platform in M0.
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
CREATE ROLE habbyte LOGIN PASSWORD '$HABBYTE_DB_PASSWORD';
CREATE DATABASE habbyte OWNER habbyte;
CREATE DATABASE habbyte_test OWNER habbyte;
CREATE ROLE keycloak LOGIN PASSWORD '$KEYCLOAK_DB_PASSWORD';
CREATE DATABASE keycloak OWNER keycloak;
SQL
