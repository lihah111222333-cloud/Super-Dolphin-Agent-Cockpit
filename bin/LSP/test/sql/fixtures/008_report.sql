WITH totals AS (
  SELECT user_id, SUM(total_cents) AS cents
  FROM orders
  GROUP BY user_id
)
SELECT users.display_name, totals.cents
FROM users
LEFT JOIN totals ON totals.user_id = users.id
ORDER BY users.display_name;
