package gaurun

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	statsGo "github.com/fukata/golang-stats-api-handler"
)

func RegisterHTTPHandlers() {
	http.HandleFunc("/push", PushNotificationHandler)
	http.HandleFunc("/stat/app", StatsHandler)
	http.HandleFunc("/config/app", ConfigHandler)
	statsGo.PrettyPrintEnabled()
	http.HandleFunc("/stat/go", statsGo.Handler)
	http.HandleFunc("/config/pushers", ConfigPushersHandler)
}

func RunHTTPServer() {
	// Listen TCP Port
	if _, err := strconv.Atoi(ConfGaurun.Core.Port); err == nil {
		http.ListenAndServe(":"+ConfGaurun.Core.Port, nil)
	}

	// Listen UNIX Socket
	if strings.HasPrefix(ConfGaurun.Core.Port, "unix:/") {
		sockPath := ConfGaurun.Core.Port[5:]
		fi, err := os.Lstat(sockPath)
		if err == nil && (fi.Mode()&os.ModeSocket) == os.ModeSocket {
			err := os.Remove(sockPath)
			if err != nil {
				log.Fatal("failed to remove " + sockPath)
			}
		}
		l, err := net.Listen("unix", sockPath)
		if err != nil {
			log.Fatal("failed to listen: " + sockPath)
		}
		http.Serve(l, nil)
	}

	log.Fatal("port parameter is invalid: " + ConfGaurun.Core.Port)
}
