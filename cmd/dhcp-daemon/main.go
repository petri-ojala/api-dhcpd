package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty"
	"github.com/gorilla/mux"
	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/server4"
	"github.com/insomniacslk/dhcp/rfc1035label"
)

// API query JSON
type dhcpQuery struct {
	HardwareAddress string `json:"mac"`
	ClientID        string `json:"client"`
}

// API Response JSON
type dhcpResponse struct {
	HardwareAddress string        `json:"mac"`
	ClientID        string        `json:"client"`
	Hostname        string        `json:"hostname"`
	ClientIP        string        `json:"ip"`
	GatewayIP       string        `json:"gateway"`
	Netmask         string        `json:"netmask"`
	Broadcast       string        `json:"broadcast"`
	Prefix          string        `json:"prefix"`
	DNS             string        `json:"dns"`
	Domain          string        `json:"domain"`
	NTP             string        `json:"ntp"`
	LeaseTime       time.Duration `json:"leasetime"`
	Description     string        `json:"description"`
	Method          string        `json:"method"`
	Slave           bool          `json:"slave"`
}

var serverID net.IP

// Command line flags
var DHCPInterface = flag.String("interface", "eth0", "DHCP Interface")
var DHCPIP = flag.String("ip", "0.0.0.0", "DHCP Interface IP Address")
var OptServerID = flag.String("id", "", "Server ID")
var Slave = flag.Bool("slave", false, "Run as slave")
var AllowSlave = flag.Bool("allow-slave", false, "Allow slave to allocate dynamic IPs")
var MasterIP = flag.String("master-ip", "", "IP address for master server")
var HeartbeatPort = flag.String("heartbeat-port", "8079", "TCP port for heartbeat")
var APIURL = flag.String("api-url", "http://127.0.0.1:8080/", "DHCP Address API URL")

//
// Check master for heart beat (HTTP /ping)
func checkHeartbeat() bool {
	// Check if master responds to heartbeat
	client := resty.New()
	resp, err := client.R().
		Get("http://" + *MasterIP + ":" + *HeartbeatPort + "/ping")

	if err != nil || resp.StatusCode() != http.StatusOK {
		return false
	}

	return true
}

//
// Response to heart beat
func responseHeartbeat(w http.ResponseWriter, i *http.Request) {
	w.WriteHeader(http.StatusOK)
}

//
// DHCP request handler
func handler(conn net.PacketConn, peer net.Addr, req *dhcpv4.DHCPv4) {
	var apiResponse dhcpResponse
	var resp *dhcpv4.DHCPv4
	var err error

	//log.Printf("DHCP-IN: %v\n", req.Summary())

	if *Slave {
		masterStatus := checkHeartbeat()
		if masterStatus {
			// Skip, master is active
			return
		}
	}

	// Get client details from API
	client := resty.New()
	_, err = client.R().
		SetBody(dhcpQuery{HardwareAddress: req.ClientHWAddr.String(), ClientID: string(req.GetOneOption(dhcpv4.OptionHostName))}).
		SetResult(&apiResponse).
		Post(*APIURL)
	//log.Printf("API: %+v\n", apiResponse)

	if *Slave && apiResponse.Slave && !*AllowSlave {
		log.Printf("Failover mode, cannot allocate new IP addresses\n")
		return
	}

	if req.OpCode != dhcpv4.OpcodeBootRequest {
		log.Printf("MainHandler4: unsupported opcode %d. Only BootRequest (%d) is supported", req.OpCode, dhcpv4.OpcodeBootRequest)
		return
	}

	resp, err = dhcpv4.NewReplyFromRequest(req)
	if err != nil {
		log.Printf("MainHandler4: failed to build reply: %v", err)
		return
	}

	switch mt := req.MessageType(); mt {
	case dhcpv4.MessageTypeDiscover:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeOffer))
	case dhcpv4.MessageTypeRequest:
		resp.UpdateOption(dhcpv4.OptMessageType(dhcpv4.MessageTypeAck))
	default:
		log.Printf("plugins/server: Unhandled message type: %v", mt)
		return
	}

	if resp != nil {
		var peer net.Addr
		if !req.GatewayIPAddr.IsUnspecified() {
			// TODO: make RFC8357 compliant
			peer = &net.UDPAddr{IP: req.GatewayIPAddr, Port: dhcpv4.ServerPort}
		} else if resp.MessageType() == dhcpv4.MessageTypeNak {
			peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
		} else if !req.ClientIPAddr.IsUnspecified() {
			peer = &net.UDPAddr{IP: req.ClientIPAddr, Port: dhcpv4.ClientPort}
		} else if req.IsBroadcast() {
			peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
		} else {
			// FIXME: we're supposed to unicast to a specific *L2* address, and an L3
			// address that's not yet assigned.
			// I don't know how to do that with this API...
			//peer = &net.UDPAddr{IP: resp.YourIPAddr, Port: dhcpv4.ClientPort}
			log.Printf("Cannot handle non-broadcast-capable unspecified peers in an RFC-compliant way. " +
				"Response will be broadcast\n")

			peer = &net.UDPAddr{IP: net.IPv4bcast, Port: dhcpv4.ClientPort}
			//resp.ClientHWAddr = req.ClientHWAddr
		}

		// Build DHCP response from API results

		if apiResponse.ClientIP != "" {
			resp.YourIPAddr = net.ParseIP(apiResponse.ClientIP)
		}
		// Server-ID (54)
		resp.Options.Update(dhcpv4.OptServerIdentifier(serverID))
		// Lease Time (51)
		resp.Options.Update(dhcpv4.OptIPAddressLeaseTime(apiResponse.LeaseTime))
		// Subnet Mask (1)
		if apiResponse.Prefix != "" {
			cidrMask, _ := strconv.Atoi(apiResponse.Prefix)
			resp.Options.Update(dhcpv4.OptSubnetMask(net.CIDRMask(cidrMask, 32)))
		}
		// Broadcast (28)
		if apiResponse.Broadcast != "" {
			resp.Options.Update(dhcpv4.OptBroadcastAddress(net.ParseIP(apiResponse.Broadcast)))
		}
		// Default Gateway (3)
		if apiResponse.GatewayIP != "" {
			resp.Options.Update(dhcpv4.OptRouter(net.ParseIP(apiResponse.GatewayIP)))
		}
		// NTP Servers (42)
		if apiResponse.NTP != "" {
			var ntpIPs []net.IP
			for _, ipAddress := range strings.Split(apiResponse.NTP, ",") {
				ntpIPs = append(ntpIPs, net.ParseIP(ipAddress))
			}
			resp.Options.Update(dhcpv4.OptNTPServers(ntpIPs...))

		}
		// Domain Search (117)
		if apiResponse.Domain != "" {
			l := &rfc1035label.Labels{
				Labels: strings.Split(apiResponse.Domain, ","),
			}
			resp.Options.Update(dhcpv4.OptDomainSearch(l))
		}
		// DNS (6)
		if apiResponse.DNS != "" {
			var dnsIPs []net.IP
			for _, ipAddress := range strings.Split(apiResponse.DNS, ",") {
				dnsIPs = append(dnsIPs, net.ParseIP(ipAddress))
			}
			resp.Options.Update(dhcpv4.OptDNS(dnsIPs...))
		}

		//log.Printf("DHCP-OUT: %v\n", resp.Summary())

		if _, err := conn.WriteTo(resp.ToBytes(), peer); err != nil {
			log.Printf("MainHandler4: conn.Write to %v failed: %v", peer, err)
		}

	} else {
		log.Print("MainHandler4: dropping request because response is nil")
	}

}

func main() {
	flag.Parse()

	serverID = net.ParseIP(*OptServerID)

	laddr := net.UDPAddr{
		IP:   net.ParseIP(*DHCPIP),
		Port: 67,
	}

	server, err := server4.NewServer(*DHCPInterface, &laddr, handler)
	if err != nil {
		log.Fatal(err)
	}

	// Heartbeat server
	router := mux.NewRouter()
	router.HandleFunc("/ping", responseHeartbeat).Methods("GET")

	srv := &http.Server{
		Addr:         "0.0.0.0:" + *HeartbeatPort,
		WriteTimeout: time.Second * 5,
		ReadTimeout:  time.Second * 5,
		IdleTimeout:  time.Second * 10,
		Handler:      router,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()

	// DHCP server
	server.Serve()
}
