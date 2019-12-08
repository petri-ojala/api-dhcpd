package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-redis/redis"
	"github.com/gorilla/mux"
)

// Connection to Redis backend
var client *redis.Client

// Default settings (restart to reload)
var defaultGroup map[string]string

// Default settings for dynamic pool (restart to reload)
var defaultPoolGroup map[string]string

// Inbound API JSON
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

var slave = flag.Bool("slave", false, "Run as slave")
var allowSlave = flag.Bool("allow-slave", false, "Allow slave to allocate dynamic IPs")

// prettyPrint for structs
func prettyPrint(i interface{}) string {
	s, _ := json.Marshal(i)
	return string(s)
}

//
// Get free Dynamic IP from "pool" hash in Redis
func getDynamicIP(mac, clientID string) string {
	if *slave && !*allowSlave {
		// Cannot allocate new addresses as slave
		return ""
	}
	dynamicPool, err := client.HGetAll("pool").Result()
	if err != nil {
		log.Print(err)
		return ""
	}
	// Find first free
	for k, v := range dynamicPool {
		if v == "" {
			// Mark dynamic IP allocated for this MAC address
			_ = client.HSet("pool", k, mac)
			// Set MAC address map for future use, client will get always the same dynamic IP
			_ = client.HSet("mac:"+mac, "ip", k)
			_ = client.HSet("mac:"+mac, "description", "dynamic:"+clientID)
			_ = client.HSet("mac:"+mac, "dynamic", true)
			_ = client.HSet("mac:"+mac, "ts", time.Now().UTC().Unix())
			//
			return k
		}
	}
	return ""
}

//
// API call
func dhcpAPI(w http.ResponseWriter, i *http.Request) {
	var q dhcpQuery
	var d map[string]string

	reqBody, err := ioutil.ReadAll(i.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err = json.Unmarshal(reqBody, &q)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("IN: %s\n", prettyPrint(q))

	g := defaultGroup

	// Build API response
	r := &dhcpResponse{}
	r.HardwareAddress = q.HardwareAddress
	r.ClientID = q.ClientID

	// Check for client ID
	d, err = client.HGetAll("client:" + q.ClientID).Result()
	if err == nil && q.ClientID != "" && len(d) > 0 {
		r.Method = "ByClientID"
		_ = client.HSet("client:"+q.ClientID, "ts", time.Now().UTC().Unix())
	} else {
		// Check for MAC address
		d, err = client.HGetAll("mac:" + q.HardwareAddress).Result()
		if err == nil && q.HardwareAddress != "" && len(d) > 0 {
			r.Method = "ByHardwareAddress"
			_ = client.HSet("mac:"+q.HardwareAddress, "ts", time.Now().UTC().Unix())
		} else {
			// Not found, allocate address from dynamic pool
			r.Method = "Dynamic"
			newIP := getDynamicIP(q.HardwareAddress, q.ClientID)
			if newIP != "" {
				r.ClientIP = newIP
			}
			g = defaultPoolGroup
		}
	}

	// If specific group is defined, overwrite default group
	if d["group"] != "" {
		g, err = client.HGetAll("group:" + d["group"]).Result()
	} else {
		g, err = client.HGetAll("group:default").Result()
	}

	// Copy group details to the response
	for k, v := range g {
		switch k {
		case "gateway":
			r.GatewayIP = v
			cidrMask, _ := strconv.Atoi(g["prefix"])
			r.Broadcast = net.ParseIP(v).Mask(net.CIDRMask(cidrMask, 32)).String()
		case "dns":
			r.DNS = v
		case "domain":
			r.Domain = v
		case "ntp":
			r.NTP = v
		case "lease-time":
			l, err := time.ParseDuration(v)
			if err == nil {
				r.LeaseTime = l
			}
		case "prefix":
			r.Prefix = v
			cidrMask, _ := strconv.Atoi(v)
			r.Netmask = net.IP(net.CIDRMask(cidrMask, 32)).String()
		}
	}

	// Copy client details to the response
	for k, v := range d {
		switch k {
		case "ip":
			r.ClientIP = v
		case "description":
			r.Description = v
		case "hostname":
			r.Hostname = v
		}
	}

	log.Printf("OUT: %s\n", prettyPrint(r))
	// Send resonse
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(r)
}

func main() {
	flag.Parse()

	// Open Redis connection
	client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// Get default group
	defaultGroup, _ = client.HGetAll("group:default").Result()
	// Get default group for dynamic pool
	defaultPoolGroup, _ = client.HGetAll("group:pool").Result()

	router := mux.NewRouter()
	router.HandleFunc("/", dhcpAPI).Methods("POST")
	log.Fatal(http.ListenAndServe(":8080", router))
}
