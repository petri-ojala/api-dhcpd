# api-dhcpd

A simple DHCP server implementation with API backend.  DHCP queries are responded based on backend that will provide the response from
a backend database.

## Background

I had been running a two server ISC dhcpd server for quite a long time and we had some discussion with colleagues about providing DHCP servers from a backend inventory database.  Initially I was thinking about the newer KEA DHCP from ISC, configured it but it felt a bit too heavy for the job and building a hook for it would have required some serious effort.

I had seen a presentation by Facebook how they deploy racks and handle DHCP load, and they have open-sourced dhcpdlb (https://github.com/facebookincubator/dhcplb), dhcp load-balancer, that is built with golang and uses Andrea Barberio's great dhcp library.  Now that Facebook announced (https://engineering.fb.com/data-infrastructure/dhcplb-server/) that they are implementing the dhcp server itself as part of dhcplb, things got a bit more interesting..

I'm not running a proper device inventory at home (;-) so I ended up building a simple Redis database to hold the data.  I can have a simple script with blocks like:

```
# Google Nest Hub (Kitchen)
redis-cli del mac:1c:f2:9a:68:49:b1
redis-cli hset mac:1c:f2:9a:68:49:b1 description 'Google Nest Hub (Kitchen)'
redis-cli hset mac:1c:f2:9a:68:49:b1 hostname 'google-hub-kitchen'
redis-cli hset mac:1c:f2:9a:68:49:b1 ip 172.17.3.130
```

I'm currently running about 75 devices with IP address in my home network.  I may consider moving to a proper device inventory at some point but we'll see..

## Usage

I current run this as a two server setup, one master and one standby server.

On the master server there is `dhcp-api` running with a Redis server configured locally.  `dhcp-daemon` is running with ID that is the IP address of the server (172.17.2.1).

On the standby server there is `dhcp-api` running with options `--slave` to enable stand-by operation, and `--allow-slave` to support dynamic IP requests.  There is Redis server running locally as read-only replica.  `dhcp-daemon` is running similar `--slave` and `--allow-slave` options, ID of 172.17.2.2, and `--master-ip` set to the master server's IP.

The standby server will see all the DHCP queries and when one is received, it will do a health check to the master and if the master doesn't respond, the standby server will provide the DHCP answer.

I have Redis configured as Master-Replica setup.  The master DHCP server runs a master Redis server and provides e.g. allocations for new dynamic IP addresses.  Both DHCP servers run a local Redis database so if the server goes down, all three components (DHCP server, DHCP API server, Redis database) go down locally.

## dhcp-daemon options

`--interface` to define which interface to provide DHCP service to, default `eth0`  
`--ip` to define IP address for the interface, default `0.0.0.0`  
`--heartbeat-port` to define port for health check port, default `8079`  
`--api-url` to define URL for the API backend, default `http://127.0.0.1:8080/`

## DHCP CLI tool

There is a simple `dhcp-cli` to check the current data from the Redis database.

`show groups` will show all response groups defined in the database.  `default` must exist, others are optional.  For example:

```
# ~pi/dhcp-cli show groups
DHCP groups:

group:pool
  lease-time = 1h
  gateway = 172.17.2.254
  broadcast = 255.255.0.0
  dns = 10.10.10.10,172.17.2.2
  domain = ojala.cloud.
  ntp = 172.17.2.22

group:default
  domain = ojala.cloud.
  lease-time = 24h
  gateway = 172.17.2.254
  broadcast = 255.255.0.0
  dns = 10.10.10.10
  prefix = 16

group:getflix
  broadcast = 255.255.0.0
  dns = 82.103.129.240,46.246.29.68
  prefix = 16
  domain = ojala.cloud.
  ntp = 172.17.2.22
  lease-time = 24h
  gateway = 172.17.2.254
```

`show clients` will show all client configurations in the database.  DHCP response can be given either by client ID or by client MAC address.  Client ID's are defined in the Redis database as `client:id`

`show mac` will show all mac addresses in the database.  For example:

```
# ~pi/dhcp-cli show mac
DHCP by Ethernet address:

  mac:dc:a6:32:43:e5:e0  172.17.7.5          2019-12-08 13:44:07  Kubernetes Node 4 (raspberry-pi4-5)
  mac:f4:f5:d8:a9:5a:b6  172.17.4.19         2019-11-03 02:53:19  Google Chromecast Ultra  (group:getflix)
  mac:18:b4:30:a5:b1:bf  172.17.3.134        2019-12-08 16:26:02  Google Nest Protect (Hallway)
  mac:e0:66:78:68:b8:31  172.17.2.184        2019-12-08 12:03:41  Apple iPad Mini (Kitchen)
  mac:00:17:88:22:03:4c  172.17.3.1          2019-12-08 15:35:00  Philips Hue hub
  mac:40:cb:c0:c9:51:d6  172.17.4.18         2019-12-08 19:01:25  Apple TV 4K (Living room)  (group:getflix)
  mac:26:fc:7f:43:d6:dc  172.17.8.1          2019-12-08 17:27:44  Google Edge TPU Board
  mac:18:b4:30:31:52:fc  172.17.3.135        2019-12-08 00:40:45  Google Nest Protect (Study)
  mac:b8:27:eb:4b:19:a2  172.17.2.24         2019-12-08 07:40:07  Raspberry Pi Zero W
  mac:b8:27:eb:37:92:e5  172.17.2.22         2019-12-08 07:26:13  GPS/NTP Server (Raspberry Pi Zero W)
  mac:dc:54:d7:d2:18:55  172.17.3.132        2019-12-08 13:47:07  Amazon Echo Show 5 (Study)
  mac:54:62:e2:06:11:4c  172.17.2.180        2019-12-08 16:52:27  Apple iPad Air (Petri)
  mac:00:80:a3:91:38:d4  172.17.254.250                           RIPE Atlas Probe
```

The Redis database entry would look something like:

```
# k8s-node-4 (raspberry-pi4-5)
redis-cli del mac:dc:a6:32:43:e5:e0
redis-cli hset mac:dc:a6:32:43:e5:e0 description 'Kubernetes Node 4 (raspberry-pi4-5)'
redis-cli hset mac:dc:a6:32:43:e5:e0 hostname 'raspberry-pi4-5'
redis-cli hset mac:dc:a6:32:43:e5:e0 ip 172.17.7.5

# Chromecast Ultra (WiFi)
redis-cli del mac:f4:f5:d8:a9:5a:b6
redis-cli hset mac:f4:f5:d8:a9:5a:b6 description 'Google Chromecast Ultra'
redis-cli hset mac:f4:f5:d8:a9:5a:b6 hostname 'chromecast-ultra'
redis-cli hset mac:f4:f5:d8:a9:5a:b6 ip 172.17.4.19
redis-cli hset mac:f4:f5:d8:a9:5a:b6 group getflix
```

(hostname is currently not used for anything)

## Packages

This tool is built on top of this package:

* `https://github.com/insomniacslk/dhcp` for the DHCPv4 implementation, used e.g. by Facebook in `dhcplb`

## Known issues

_It works on my computer_, this has been running quite fine on my home network but no doubt there can be, and probably is, bugs in the code, or it could be improved quite a bit.  Ideally the master/slave setup should be replaced with a proper cluster configuration where nodes can also perform load-balancing, and I would like to run part of the cluster within my Kubernetes cluster.  The backend should also include caching.

The setup has not been tested in an environment where the DHCP request is forwarded to different network by the switch/router.  DHCPv6 is not supported but would be easy to add as the dhcp library has support for it.

