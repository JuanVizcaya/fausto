-- Runs once, when the residents-db volume is first initialized.
-- Integration tests use a separate database so they never touch dev data.
CREATE DATABASE residents_test OWNER residents;
