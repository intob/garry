git pull
sudo cp -f garry.service.conf /etc/systemd/system/garry.service
/usr/local/go/bin/go build .
sudo systemctl daemon-reload
sudo systemctl enable garry.service
sudo systemctl stop garry.service
sudo systemctl start garry.service