CREATE TRIGGER reject_inactive_order
BEFORE INSERT ON orders
WHEN (SELECT active FROM users WHERE id = NEW.user_id) = 0
BEGIN
  SELECT RAISE(ABORT, 'inactive user');
END;
