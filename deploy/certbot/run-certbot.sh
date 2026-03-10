#!/bin/sh
set -eu

domain="${PUBLIC_DOMAIN}"
email="${LETSENCRYPT_EMAIL}"
webroot="/var/www/certbot"
cert_path="/etc/letsencrypt/live/${domain}/fullchain.pem"
reload_flag="${webroot}/.nginx-reload"

issue_initial_cert() {
    certbot certonly \
        --webroot \
        -w "${webroot}" \
        -d "${domain}" \
        -m "${email}" \
        --agree-tos \
        --no-eff-email \
        || return 1

    touch "${reload_flag}"
}

renew_certs() {
    certbot renew \
        --webroot \
        -w "${webroot}" \
        --deploy-hook "touch ${reload_flag}" \
        --quiet \
        || return 1
}

while :; do
    if [ ! -f "${cert_path}" ]; then
        issue_initial_cert || true
    else
        renew_certs || true
    fi

    sleep 12h
done
