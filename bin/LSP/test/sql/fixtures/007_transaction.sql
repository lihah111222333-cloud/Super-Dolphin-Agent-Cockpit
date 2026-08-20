BEGIN TRANSACTION;

UPDATE users
SET active = 1
WHERE display_name = 'Ada';

COMMIT;
