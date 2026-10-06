-- Tarmoq izolyatsiyasi sozlamasi ham sinxronlanadi (sandbox bilan bir xil
-- sabab: tiklangan ilova himoyasiz qaytib qolmasligi kerak).
ALTER TABLE apps ADD COLUMN net_isolate BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE apps ADD COLUMN net_ports   TEXT    NOT NULL DEFAULT '';
