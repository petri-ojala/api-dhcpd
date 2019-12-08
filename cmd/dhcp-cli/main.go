package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/spf13/cobra"
)

var client *redis.Client
var keys []string

func main() {
	// Open Redis connection
	client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// Get all keys
	keys, _ = client.Keys("*").Result()

	rootCmd := &cobra.Command{Use: "dhcp-cli"}

	//cobra.OnInitialize(initCLI)

	var cmdShow = &cobra.Command{
		Use:   "show",
		Short: "Show information",
	}

	var cmdShowGroups = &cobra.Command{
		Use:   "groups",
		Short: "Show all DHCP groups",
		Run: func(cmd *cobra.Command, args []string) {
			dhcpShowGroups(args)
		},
	}

	var cmdShowClients = &cobra.Command{
		Use:   "clients",
		Short: "Show all DHCP clients",
		Run: func(cmd *cobra.Command, args []string) {
			dhcpShowClients(args)
		},
	}

	var cmdShowHWAddresses = &cobra.Command{
		Use:   "mac",
		Short: "Show all DHCP MAC addresses",
		Run: func(cmd *cobra.Command, args []string) {
			dhcpShowHWAddresses(args)
		},
	}

	rootCmd.AddCommand(cmdShow)
	cmdShow.AddCommand(cmdShowGroups)
	cmdShow.AddCommand(cmdShowClients)
	cmdShow.AddCommand(cmdShowHWAddresses)

	rootCmd.Execute()
}

func dhcpShowGroups(args []string) {
	fmt.Printf("DHCP groups:\n\n")
	for _, k := range keys {
		if strings.HasPrefix(k, "group:") {
			fmt.Printf("%s\n", k)
			d, _ := client.HGetAll(k).Result()
			for k, v := range d {
				fmt.Printf("  %s = %s\n", k, v)
			}
			fmt.Print("\n")
		}
	}
}

func dhcpShowClients(args []string) {
	// By ClientID
	fmt.Printf("DHCP Clients:\n\n")
	for _, k := range keys {
		if strings.HasPrefix(k, "client:") {
			fmt.Printf("%s\n", k)
			d, _ := client.HGetAll(k).Result()
			for k, v := range d {
				fmt.Printf("  %s = %s\n", k, v)
			}
		}
	}
}

func dhcpShowHWAddresses(args []string) {
	// By HardwareAddress
	fmt.Printf("DHCP by Ethernet address:\n\n")
	for _, k := range keys {
		if strings.HasPrefix(k, "mac:") {
			d, _ := client.HGetAll(k).Result()
			fmt.Printf("  %s  %-16s", k, d["ip"])
			if d["dynamic"] == "1" {
				fmt.Printf("* ")
			} else {
				fmt.Printf("  ")
			}
			if d["ts"] != "" {
				i, _ := strconv.ParseInt(d["ts"], 10, 64)
				tm := time.Unix(i, 0)
				fmt.Printf("  %s", tm.Format("2006-01-02 15:04:05"))
			} else {
				fmt.Print("                     ")
			}
			if d["description"] != "" {
				fmt.Printf("  %s", d["description"])
			}
			if d["group"] != "" {
				fmt.Printf("  (group:%s)", d["group"])
			}
			fmt.Print("\n")
		}
	}
}
