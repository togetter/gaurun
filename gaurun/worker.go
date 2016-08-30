package gaurun

import (
	"strings"
	"sync/atomic"

	"github.com/RobotsAndPencils/buford/push"
)

var (
	PusherCount int64
)

func init() {
	PusherCount = 0
}

func StartPushWorkers(workerNum, queueNum int64) {
	QueueNotification = make(chan RequestGaurunNotification, queueNum)
	for i := int64(0); i < workerNum; i++ {
		go pushNotificationWorker()
	}
}

func isExternalServerError(err error, platform int) bool {
	switch platform {
	case PlatFormIos:
		if err == push.ErrIdleTimeout || err == push.ErrShutdown || err == push.ErrInternalServerError || err == push.ErrServiceUnavailable {
			return true
		}
	case PlatFormAndroid:
		if err.Error() == "Unavailable" || err.Error() == "InternalServerError" || strings.Contains(err.Error(), "Timeout") {
			return true
		}
	default:
		// not through
	}
	return false
}

func pushSync(pusher func(req RequestGaurunNotification) error, req RequestGaurunNotification, retryMax int) {
Retry:
	err := pusher(req)
	if err != nil && req.Retry < retryMax && isExternalServerError(err, req.Platform) {
		req.Retry++
		goto Retry
	}
}

func pushAsync(pusher func(req RequestGaurunNotification) error, req RequestGaurunNotification, retryMax int) {
Retry:
	err := pusher(req)
	if err != nil && req.Retry < retryMax && isExternalServerError(err, req.Platform) {
		req.Retry++
		goto Retry
	}

	atomic.AddInt64(&PusherCount, -1)
}

func pushNotificationWorker() {
	var (
		retryMax int
		pusher   func(req RequestGaurunNotification) error
	)

	for {
		notification := <-QueueNotification

		switch notification.Platform {
		case PlatFormIos:
			pusher = pushNotificationIos
			retryMax = ConfGaurun.Ios.RetryMax
		case PlatFormAndroid:
			pusher = pushNotificationAndroid
			retryMax = ConfGaurun.Android.RetryMax
		default:
			LogError.Warnf("invalid platform: %d", notification.Platform)
			continue
		}

		if atomic.LoadInt64(&ConfGaurun.Core.PusherMax) <= 0 {
			pushSync(pusher, notification, retryMax)
			continue
		}

		if atomic.LoadInt64(&PusherCount) < atomic.LoadInt64(&ConfGaurun.Core.PusherMax) {
			// Do not increment PusherCount in pushAsync().
			// Because PusherCount is sometimes over pusherMax
			// as the increment in goroutine runs asynchronously.
			atomic.AddInt64(&PusherCount, 1)

			go pushAsync(pusher, notification, retryMax)
			continue
		} else {
			pushSync(pusher, notification, retryMax)
			continue
		}
	}
}
