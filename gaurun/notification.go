package gaurun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/togetter/gcm"
)

type RequestGaurun struct {
	Notifications []RequestGaurunNotification `json:"notifications"`
}

type RequestGaurunNotification struct {
	// Common
	Tokens   []string `json:"token"`
	Platform int      `json:"platform"`
	Message  string   `json:"message"`
	// Android
	CollapseKey    string `json:"collapse_key,omitempty"`
	DelayWhileIdle bool   `json:"delay_while_idle,omitempty"`
	TimeToLive     int    `json:"time_to_live,omitempty"`
	// iOS
	Badge            int          `json:"badge,omitempty"`
	Sound            string       `json:"sound,omitempty"`
	Icon             string       `json:"icon,omitempty"`
	Color            string       `json:"color,omitempty"`
	ContentAvailable bool         `json:"content_available,omitempty"`
	Expiry           int          `json:"expiry,omitempty"`
	Retry            int          `json:"retry,omitempty"`
	Extend           []ExtendJSON `json:"extend,omitempty"`
	// meta
	ID uint64 `json:"seq_id,omitempty"`
}

type ExtendJSON struct {
	Key   string `json:"key"`
	Value string `json:"val"`
}

type ResponseGaurun struct {
	Message string `json:"message"`
}

type CertificatePem struct {
	Cert []byte
	Key  []byte
}

func InitHttpClient() error {
	TransportGCM := &http.Transport{
		MaxIdleConnsPerHost: ConfGaurun.Android.KeepAliveConns,
		Dial: (&net.Dialer{
			Timeout:   time.Duration(ConfGaurun.Android.Timeout) * time.Second,
			KeepAlive: time.Duration(ConfGaurun.Android.KeepAliveTimeout) * time.Second,
		}).Dial,
	}
	GCMClient = &gcm.Sender{
		ApiKey: ConfGaurun.Android.ApiKey,
		Http: &http.Client{
			Transport: TransportGCM,
			Timeout:   time.Duration(ConfGaurun.Android.Timeout) * time.Second,
		},
	}

	var err error
	APNSClient, err = NewApnsClientHttp2(
		ConfGaurun.Ios.PemCertPath,
		ConfGaurun.Ios.PemKeyPath,
	)
	if err != nil {
		return err
	}
	return nil
}

func enqueueNotifications(notifications []RequestGaurunNotification) {
	for _, notification := range notifications {
		err := validateNotification(&notification)
		if err != nil {
			LogError.Error(err)
			continue
		}
		var enabledPush bool
		switch notification.Platform {
		case PlatFormIos:
			enabledPush = ConfGaurun.Ios.Enabled
		case PlatFormAndroid:
			enabledPush = ConfGaurun.Android.Enabled
		}
		if enabledPush {
			// Enqueue notification per token
			for _, token := range notification.Tokens {
				notification2 := notification
				notification2.Tokens = []string{token}
				notification2.ID = numberingPush()
				LogPush(notification2.ID, StatusAcceptedPush, token, 0, notification2, nil)
				QueueNotification <- notification2
			}
		}
	}
}

func classifyByDevice(reqGaurun *RequestGaurun) ([]RequestGaurunNotification, []RequestGaurunNotification) {
	var (
		reqGaurunNotificationIos     []RequestGaurunNotification
		reqGaurunNotificationAndroid []RequestGaurunNotification
	)
	for _, notification := range reqGaurun.Notifications {
		switch notification.Platform {
		case PlatFormIos:
			reqGaurunNotificationIos = append(reqGaurunNotificationIos, notification)
		case PlatFormAndroid:
			reqGaurunNotificationAndroid = append(reqGaurunNotificationAndroid, notification)
		}
	}
	return reqGaurunNotificationIos, reqGaurunNotificationAndroid
}

func pushNotificationIos(req RequestGaurunNotification) error {
	LogError.Debug("START push notification for iOS")

	service := NewApnsServiceHttp2(APNSClient)

	token := req.Tokens[0]

	headers := NewApnsHeadersHttp2(&req)
	payload := NewApnsPayloadHttp2(&req)

	stime := time.Now()
	err := ApnsPushHttp2(token, service, headers, payload)

	etime := time.Now()
	ptime := etime.Sub(stime).Seconds()

	if err != nil {
		atomic.AddInt64(&StatGaurun.Ios.PushError, 1)
		LogPush(req.ID, StatusFailedPush, token, ptime, req, err)
		return err
	}

	atomic.AddInt64(&StatGaurun.Ios.PushSuccess, 1)
	LogPush(req.ID, StatusSucceededPush, token, ptime, req, nil)

	LogError.Debug("END push notification for iOS")

	return nil
}

func pushNotificationAndroid(req RequestGaurunNotification) error {
	LogError.Debug("START push notification for Android")

	data := map[string]interface{}{"message": req.Message}
	if len(req.Extend) > 0 {
		for _, extend := range req.Extend {
			data[extend.Key] = extend.Value
		}
	}

	token := req.Tokens[0]

	notification := &gcm.Notification{
		Body:  req.Message,
		Sound: req.Sound,
		Icon:  req.Icon,
		Color: req.Color,
	}

	msg := gcm.NewMessageWithNotification(data, notification, token)
	msg.CollapseKey = req.CollapseKey
	msg.DelayWhileIdle = req.DelayWhileIdle
	msg.TimeToLive = req.TimeToLive

	stime := time.Now()
	resp, err := GCMClient.SendNoRetry(msg)
	etime := time.Now()
	ptime := etime.Sub(stime).Seconds()
	if err != nil {
		atomic.AddInt64(&StatGaurun.Android.PushError, 1)
		LogPush(req.ID, StatusFailedPush, token, ptime, req, err)
		return err
	}

	if resp.Failure > 0 {
		atomic.AddInt64(&StatGaurun.Android.PushSuccess, int64(resp.Success))
		atomic.AddInt64(&StatGaurun.Android.PushError, int64(resp.Failure))
		LogPush(req.ID, StatusFailedPush, token, ptime, req, errors.New(resp.Results[0].Error))
		return errors.New(resp.Results[0].Error)
	}

	LogPush(req.ID, StatusSucceededPush, token, ptime, req, nil)

	atomic.AddInt64(&StatGaurun.Android.PushSuccess, int64(len(req.Tokens)))
	LogError.Debug("END push notification for Android")

	return nil
}

func validateNotification(notification *RequestGaurunNotification) error {

	for _, token := range notification.Tokens {
		if len(token) == 0 {
			return errors.New("empty token")
		}
	}

	if notification.Platform < 1 || notification.Platform > 2 {
		return errors.New("invalid platform")
	}

	if len(notification.Message) == 0 {
		return errors.New("empty message")
	}

	return nil
}

func sendResponse(w http.ResponseWriter, msg string, code int) {
	var respGaurun ResponseGaurun

	msgJson := "{\"message\":\"" + msg + "\"}"

	err := json.Unmarshal([]byte(msgJson), &respGaurun)
	if err != nil {
		msgJson = "{\"message\":\"Response-body could not be created\"}"
	}

	w.WriteHeader(code)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Server", serverHeader())

	fmt.Fprint(w, msgJson)
}

func PushNotificationHandler(w http.ResponseWriter, r *http.Request) {
	LogAcceptedRequest("/push", r.Method, r.Proto, r.ContentLength)
	LogError.Debug("push-request is Accepted")

	LogError.Debug("method check")
	if r.Method != "POST" {
		sendResponse(w, "method must be POST", http.StatusBadRequest)
		return
	}

	LogError.Debug("content-length check")
	if r.ContentLength == 0 {
		sendResponse(w, "request body is empty", http.StatusBadRequest)
		return
	}

	var (
		reqGaurun RequestGaurun
		err       error
	)

	if ConfGaurun.Log.Level == "debug" {
		reqBody, err := ioutil.ReadAll(r.Body)
		if err != nil {
			sendResponse(w, "failed to read request-body", http.StatusInternalServerError)
			return
		}
		LogError.Debugf("parse request body: %s", reqBody)
		err = json.Unmarshal(reqBody, &reqGaurun)
	} else {
		LogError.Debug("parse request body")
		err = json.NewDecoder(r.Body).Decode(&reqGaurun)
	}

	if err != nil {
		LogError.Error(err)
		sendResponse(w, "Request-body is malformed", http.StatusBadRequest)
		return
	}

	if len(reqGaurun.Notifications) == 0 {
		LogError.Error("empty notification")
		sendResponse(w, "empty notification", http.StatusBadRequest)
		return
	} else if int64(len(reqGaurun.Notifications)) > ConfGaurun.Core.NotificationMax {
		msg := fmt.Sprintf("number of notifications(%d) over limit(%d)", len(reqGaurun.Notifications), ConfGaurun.Core.NotificationMax)
		LogError.Error(msg)
		sendResponse(w, msg, http.StatusBadRequest)
		return
	}

	LogError.Debug("enqueue notification")
	go enqueueNotifications(reqGaurun.Notifications)

	LogError.Debug("response to client")
	sendResponse(w, "ok", http.StatusOK)
}
