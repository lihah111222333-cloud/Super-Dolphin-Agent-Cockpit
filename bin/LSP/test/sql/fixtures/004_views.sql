CREATE VIEW active_order_totals AS
SELECT
  users.id AS user_id,
  users.display_name,
  SUM(orders.total_cents) AS total_cents
FROM users
JOIN orders ON orders.user_id = users.id
WHERE users.active = 1
GROUP BY users.id, users.display_name;
