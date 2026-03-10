#!/bin/sh
set -eu

cert_dir="/etc/letsencrypt/live/${PUBLIC_DOMAIN}"
reload_flag="/var/www/certbot/.nginx-reload"
current_mode=""

choose_template() {
    if [ -f "${cert_dir}/fullchain.pem" ] && [ -f "${cert_dir}/privkey.pem" ]; then
        echo "tls"
    else
        echo "bootstrap"
    fi
}

render_config() {
    mode="$(choose_template)"
    template="/etc/nginx/templates/production-${mode}.conf.template"
    envsubst '$PUBLIC_DOMAIN' < "${template}" > /etc/nginx/conf.d/default.conf
    current_mode="${mode}"
}

render_config
nginx -g 'daemon off;' &
nginx_pid=$!

while kill -0 "${nginx_pid}" 2>/dev/null; do
    sleep 30

    next_mode="$(choose_template)"
    if [ "${next_mode}" != "${current_mode}" ] || [ -f "${reload_flag}" ]; then
        render_config
        nginx -s reload
        rm -f "${reload_flag}"
    fi
done

wait "${nginx_pid}"
