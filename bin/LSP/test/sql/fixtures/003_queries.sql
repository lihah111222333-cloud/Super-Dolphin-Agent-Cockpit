SELECT
  users.display_name,
  orders.total_cents
FROM users
JOIN orders ON orders.user_id = users.id
WHERE users.active = 1
ORDER BY orders.total_cents DESC;
