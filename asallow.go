package main

import (
	"code.google.com/p/gcfg"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
    "net"
)

const PREFIX_URI = "https://stat.ripe.net/data/announced-prefixes/data.json?resource="

var ipset_count int = 0
var ipset_string string = ""
var ipset6_string string = ""

type config struct {
	Main struct {
		Allow []string
		ASN   []string
	}
}

func readconfig(cfgfile string) config {
    var cfg config
	content, err := ioutil.ReadFile(cfgfile)
	if err != nil {
		log.Fatal(err)
	}
	err = gcfg.ReadStringInto(&cfg, string(content))
	if err != nil {
		log.Fatal("Failed to parse "+cfgfile+":", err)
	}
	return cfg
}

func getAS(ASnumber string) []byte {
	fmt.Println("fetching ASN: " + ASnumber)
	resp, err := http.Get(PREFIX_URI + ASnumber)
	if err != nil {
		log.Fatal("site not available")
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal("can not read body")
	}
	return body
}

func isIpOrCidr (ipcidr string) *net.IP {
    ip,_,err := net.ParseCIDR(ipcidr)
    if (err != nil) {
        ip = net.ParseIP(ipcidr)
        if (ip == nil) {
            return nil
        }
    }
    return &ip
}


func doipset() {
    ipset_header := "create AS_allow hash:net family inet comment\n"
    ipset_header += "create AS_allow6 hash:net family inet6 comment\n"
    ipset_header += "create AS_allow_swap hash:net family inet comment\n"
    ipset_header += "create AS_allow_swap6 hash:net family inet6 comment\n"
    ipset_footer := "swap AS_allow AS_allow_swap\n"
    ipset_footer += "swap AS_allow6 AS_allow_swap6\n"
    ipset_footer += "destroy AS_allow_swap\n"
    ipset_footer += "destroy AS_allow_swap6\n"
    ipset_string = ipset_header + ipset_string + ipset_footer
	cmd := exec.Command("ipset", "-!", "restore")
	cmd.Stdin = strings.NewReader(ipset_string)
	out,err := cmd.CombinedOutput()
	if err != nil {
		log.Println("ipset restore failed (see below)")
        log.Fatal(string(out))
	}
}

func parseBody(body []byte, ASnumber string, sc chan string) {
	ipset_string := ""
	dec := json.NewDecoder(strings.NewReader(string(body)))
	var mapstring map[string]interface{}
	if err := dec.Decode(&mapstring); err != nil {
		log.Fatal(err)
	}
	datamap := mapstring["data"]
	mapstring = datamap.(map[string]interface{})
	prefixes := mapstring["prefixes"]
	prefixes_array := prefixes.([]interface{})
	for _, prefix_element := range prefixes_array {
		mapstring = prefix_element.(map[string]interface{})
        prefix := mapstring["prefix"].(string)
        ip := isIpOrCidr(prefix) // input validation
        if ip != nil { // it really is an IP 
            if ip.To4() != nil { // is it IPv4
                ipset_string += "add AS_allow_swap " + prefix + " comment AS" + ASnumber + "\n"
            } else { // ipv6
                ipset_string += "add AS_allow_swap6 " + prefix + " comment AS" + ASnumber + "\n"
            }
            ipset_count += 1
        } else {
            log.Println("not an ip (range): "+prefix)
        }
	}
	//fmt.Println("starting thread for: "+ASnumber)
	sc <- ipset_string
}

func addAllowed(allowed []string) {
	for _, el := range allowed {
        ip := isIpOrCidr(el)
        if ip != nil { //really an IP
            if ip.To4() != nil {
                ipset_string += "add AS_allow_swap " + el + " comment \"read from asallow.conf\"\n"
            } else {
                ipset_string += "add AS_allow_swap6 " + el + " comment \"read from asallow.conf\"\n"
            }
            ipset_count += 1
        } else {
            log.Println("not an ip (range): "+el)
        }
	}
}

func main() {
	if os.Geteuid() != 0 {
		log.Fatal("This needs to be run as root")
	}
	cfgfile := flag.String("conf", "asallow.conf", "a valid config file")
	flag.Parse()
	sc := make(chan string)
	cfg := readconfig(*cfgfile)
	addAllowed(cfg.Main.Allow)
	for i, ASN := range cfg.Main.ASN {
		if i > 0 && i%2 == 0 { // max 2 rqs
			time.Sleep(time.Second)
		}
		go func(ASN string) {
			body := getAS(ASN)
			go parseBody(body, ASN, sc)
		}(ASN)
	}
	for range cfg.Main.ASN {
		ipset_string += <-sc
	}
	doipset()

	fmt.Printf("%v subnets added\n", ipset_count)
	fmt.Println("AS_allow and AS_allow6 ipset created/modified")
}
