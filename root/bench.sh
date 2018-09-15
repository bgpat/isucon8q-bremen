#!/bin/bash

set -ue

mysqldumpslow -s t /var/log/mariadb/mariadb-slow.log | slackcat --filename "slow-$(hostname).log"
cp /var/log/mariadb/mariadb-slow.log /var/log/mariadb/mariadb-slow.log.old
rm /var/log/mariadb/mariadb-slow.log
systemctl restart mariadb
