-- Ilovalarning sandbox sozlamasi ham sinxronlanadi: bulutdan tiklangan ilova
-- izolyatsiyasiz qaytib qolmasligi kerak.
ALTER TABLE apps ADD COLUMN sandbox    BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE apps ADD COLUMN sandbox_ro TEXT    NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN sandbox_rw TEXT    NOT NULL DEFAULT '';
