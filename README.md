# golkv373

Binary for receiving MJPEG streams from Lenkeng LKV373 devices.

Just run the binary and open webinterface on http://localhost:8080/

- logging to file with name `log-<date>.txt`:
  - set env `GOLKV_LOG=1`
- change listen ip/port:
  - set `GOLKV_LISTEN` to e.g. `:80`, `127.0.0.1:1234`, `192.168.23.1:80`,...

## Required settings
1. correct multicast route set for interface:
  - verify with `ip route get 226.2.2.2` - you should see the interface where you device is connected
  - if not set correctly, fix with `sudo ip route add 226.2.2.2 dev <correct_interface>`
2. device web interface is accessible
  - find out the ip of your device by command `sudo tcpdump -i any -nn port 48689`
  - try to connect to http://<ip_of_your_device>/
  - if it doesn't work, change the ip of your network adapter to match the subnet of your device and try again
  - you can configure different device IP via the web interface afterwards
3. Firewall allows ports `48689` and `2068`

## FAQ
- Q: I am seeing `No active transmitters`, what to do?
  - A: No advertisments are being received on port `48689`, verify with `sudo tcpdump -i any -nn port 48689`. Verify the connection of your device.

- Q: I am seeing `Warning: keepalive sent to <address>, but no data received, consult FAQ`
  - A: Your device sends advertisments but no data can be received. Check all steps in required settings. 
