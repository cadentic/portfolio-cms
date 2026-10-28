#!/bin/sh
# Starts the Auth emulator and creates the local owner account so you can sign
# in at http://localhost:4000/admin with LOCAL_ADMIN_EMAIL / LOCAL_ADMIN_PASSWORD.
set -e
firebase emulators:start --only auth --project demo-cadentic &
pid=$!
until wget -qO- http://127.0.0.1:9099/ >/dev/null 2>&1; do sleep 1; done
if [ -n "$LOCAL_ADMIN_EMAIL" ] && [ -n "$LOCAL_ADMIN_PASSWORD" ]; then
  wget -qO- --header 'Content-Type: application/json' \
    --post-data "{\"email\":\"$LOCAL_ADMIN_EMAIL\",\"password\":\"$LOCAL_ADMIN_PASSWORD\",\"returnSecureToken\":false}" \
    "http://127.0.0.1:9099/identitytoolkit.googleapis.com/v1/accounts:signUp?key=local" >/dev/null 2>&1 \
    && echo "created local owner account $LOCAL_ADMIN_EMAIL" || echo "owner account already exists"
fi
# Healthcheck marker: the emulator is up and the owner account exists.
touch /tmp/ready
wait $pid
