-- Runs once, when the administrators-db volume is first initialized.
-- Integration tests use a separate database so they never touch dev data.
CREATE DATABASE administrators_test OWNER administrators;
