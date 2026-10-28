#!/bin/sh
# uploads the built react app to the s3 bucket (minio), like a deploy step would.
# it uses the normal aws cli, so the same commands work against real amazon s3
# (just drop --endpoint-url).
# hashed files get a 1 year cache; index.html gets a short one so new deploys
# show up quickly.
set -eu

S3="aws --endpoint-url http://minio:9000"
BUCKET=static

# wait until minio is up and accepts our credentials
until $S3 s3 ls >/dev/null 2>&1; do
  echo "waiting for minio..."
  sleep 2
done

# create the bucket (ignore "already exists" on re-runs)
$S3 s3 mb "s3://$BUCKET" 2>/dev/null || true

# allow anyone to read files, like a public bucket behind a cdn
$S3 s3api put-bucket-policy --bucket "$BUCKET" --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"AWS": ["*"]},
    "Action": ["s3:GetObject"],
    "Resource": ["arn:aws:s3:::'"$BUCKET"'/*"]
  }]
}'

# file names in /assets contain a content hash, so they never change
$S3 s3 cp /dist/assets "s3://$BUCKET/assets" --recursive \
  --cache-control "public, max-age=31536000, immutable"

# index.html points at the newest hashed files, so keep its cache short
$S3 s3 cp /dist/index.html "s3://$BUCKET/index.html" \
  --cache-control "public, max-age=30"

echo "static site uploaded:"
$S3 s3 ls "s3://$BUCKET" --recursive
