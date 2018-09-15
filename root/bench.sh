#!/bin/bash

set -ue

mysqldumpslow -s t /var/log/mariadb/mariadb-slow.log | head -100 | slackcat --filename "slow-$(hostname).txt"
cp /var/log/mariadb/mariadb-slow.log /var/log/mariadb/mariadb-slow.log.old
rm /var/log/mariadb/mariadb-slow.log
systemctl restart mariadb

cat /var/log/nginx/kataribe.log | kataribe [-f /root/kataribe.toml] | slackcat --filename "kataribe-$(hostname).txt"
cp /var/log/nginx/kataribe.log /var/log/nginx/kataribe.log.old
rm -f /var/log/nginx/kataribe.log
systemctl restart nginx
