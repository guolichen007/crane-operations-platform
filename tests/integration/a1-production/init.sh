#!/bin/sh
set -eu
umask 077
cp -R /input/. /fixture/
chmod 0711 /fixture
chmod 0711 /fixture/secrets
chmod 0600 /fixture/secrets/* /fixture/inventory.json /fixture/fleet.json /fixture/acl
chown 10001:10001 /fixture/inventory.json /fixture/fleet.json
chown 1883:1883 /fixture/acl
for file in app_db_password oidc_client_secret session_server_secret; do chown 10001:10001 "/fixture/secrets/$file"; done
for file in keycloak_db_password keycloak_bootstrap_password; do chown 1000:1000 "/fixture/secrets/$file"; done
# Keycloak base entrypoint reads these file names directly under /run/secrets.
ln -sf secrets/keycloak_db_password /fixture/keycloak_db_password
ln -sf secrets/keycloak_bootstrap_password /fixture/keycloak_bootstrap_password
for folder in cloud broker ingress; do
  chmod 0755 "/fixture/$folder"
  chmod 0644 "/fixture/$folder/"*.crt
  chmod 0600 "/fixture/$folder/"*.key
done
chown -R 10001:10001 /fixture/cloud
chown -R 1883:1883 /fixture/broker
chmod -R u=rwX,go= /fixture/devices
chmod 0644 /fixture/ingress.conf /fixture/mosquitto.conf
chmod 0755 /fixture/a1-fixture
