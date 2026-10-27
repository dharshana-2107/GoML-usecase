-- reserve.lua runs inside redis as one single atomic step.
-- redis is single threaded and never runs another command in the middle of
-- a script, so "check stock" and "take one" can not be split by another buyer.
-- this is exactly what the old "select then update" code got wrong.
--
-- KEYS[1] stock    (integer, units left)
-- KEYS[2] buyers   (set of user ids who already reserved)
-- KEYS[3] idem     (hash: idempotency key -> reservation id)
-- KEYS[4] owner    (hash: reservation id -> user id)
-- KEYS[5] state    (hash: reservation id -> pending | confirmed | rejected)
-- ARGV[1] user id, ARGV[2] idempotency key, ARGV[3] new reservation id
--
-- returns {code, reservation id}
--   1 = reserved, 2 = replay of an earlier request
--  -1 = sold out, -2 = user already bought, -3 = key used by another user

-- a retry with the same idempotency key gets the same answer back
local existing = redis.call('HGET', KEYS[3], ARGV[2])
if existing then
  if redis.call('HGET', KEYS[4], existing) ~= ARGV[1] then
    return {-3, ''}
  end
  return {2, existing}
end

-- one phone per user
if redis.call('SISMEMBER', KEYS[2], ARGV[1]) == 1 then
  return {-2, ''}
end

-- the actual stock check and decrement
local stock = tonumber(redis.call('GET', KEYS[1]) or '0')
if stock <= 0 then
  return {-1, ''}
end

redis.call('DECR', KEYS[1])
redis.call('SADD', KEYS[2], ARGV[1])
redis.call('HSET', KEYS[3], ARGV[2], ARGV[3])
redis.call('HSET', KEYS[4], ARGV[3], ARGV[1])
redis.call('HSET', KEYS[5], ARGV[3], 'pending')
return {1, ARGV[3]}
