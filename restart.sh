git pull
rm client/commit
git rev-parse HEAD > static/commit
cp -f garry.service.conf /etc/systemd/system/garry.service
/usr/local/go/bin/go build .
systemctl daemon-reload
systemctl enable garry.service
systemctl stop garry.service
systemctl start garry.service
