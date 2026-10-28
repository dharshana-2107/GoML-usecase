-- release.lua gives a unit back when we could not hand the order to the queue.
-- it only undoes a reservation that is still "pending", so running it twice
-- (or after the worker already confirmed the order) is harmless.
--
-- KEYS are the same six keys as reserve.lua
-- ARGV[1] user id, ARGV[2] idempotency key, ARGV[3] reservation id

if redis.call('HGET', KEYS[5], ARGV[3]) ~= 'pending' then
  return 0
end

redis.call('INCR', KEYS[1])
redis.call('SREM', KEYS[2], ARGV[1])
redis.call('HDEL', KEYS[3], ARGV[2])
redis.call('HDEL', KEYS[4], ARGV[3])
redis.call('HDEL', KEYS[5], ARGV[3])
redis.call('HINCRBY', KEYS[6], 'reserved', -1)
return 1
