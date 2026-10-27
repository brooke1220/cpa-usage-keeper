package quota

import (
	"errors"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

func (s *Service) newAutoRefreshTimer(delay time.Duration) (<-chan time.Time, func()) {
	s.autoRefreshTimerMu.Lock()
	defer s.autoRefreshTimerMu.Unlock()

	if !s.autoRefreshTimerUnavailable {
		newTimer := s.autoRefreshNewTimer
		if newTimer == nil {
			newTimer = newSuspendAwareTimer
		}
		timerCh, stopTimer, err := newTimer(delay)
		if err == nil {
			return timerCh, stopTimer
		}
		if suspendAwareTimerUnavailable(err) {
			// 内核缺少支持或运行环境禁止调用时，继续重试只会每分钟重复打印相同警告。
			s.autoRefreshTimerUnavailable = true
			logrus.WithError(err).Warn("suspend-aware timer unavailable, using standard timers until service restart")
		} else {
			// 文件描述符耗尽等临时故障仍在下一次等待时重试，避免永久失去休眠感知能力。
			logrus.WithError(err).Warn("failed to create suspend-aware timer, falling back to standard timer")
		}
	}

	timer := time.NewTimer(delay)
	return timer.C, func() { timer.Stop() }
}

func suspendAwareTimerUnavailable(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOSYS) ||
		errors.Is(err, syscall.EOPNOTSUPP) ||
		errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES)
}
