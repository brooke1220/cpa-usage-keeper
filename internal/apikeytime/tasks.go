package apikeytime

import (
	"sort"
	"time"
)

// DefaultTaskIdleMinutes 是任务时长的默认空闲阈值：同一任务内相邻请求的空档不超过它时，
// 视为本地执行脚本、操作电脑等仍在进行；超过则视为中断，空档不计入。
const DefaultTaskIdleMinutes = 10

// TaskIdleMinuteOptions 是页面可选的空闲阈值，接口只接受这些值。
var TaskIdleMinuteOptions = []int{5, 10, 30, 60}

// ValidTaskIdleMinutes 判断空闲阈值是否在可选范围内。
func ValidTaskIdleMinutes(minutes int) bool {
	for _, option := range TaskIdleMinuteOptions {
		if option == minutes {
			return true
		}
	}
	return false
}

// TaskRequest 是一次请求在时间线上占用的区间 [StartMS, EndMS)，单位为 Unix 毫秒。
// SessionID 为空的请求按 API Key 归为同一个任务。
type TaskRequest struct {
	APIKeyID int64
	// APIGroupKey keeps distinct unsynced keys apart when APIKeyID is zero.
	APIGroupKey     string
	SessionID       string
	ParentSessionID string
	StartMS         int64
	EndMS           int64
}

// TaskDay 是某个 API Key 在一个统计日内的任务时长，按工作 / 非工作时段拆分。
type TaskDay struct {
	APIKeyID int64
	// StatDay 是统计日标签距 1970-01-01 的天数，与 repository.APIKeyTimeUsageDay 一致。
	StatDay      int64
	DaytimeMS    int64
	AfterHoursMS int64
}

type taskGroupKey struct {
	apiKeyID        int64
	root            string
	unattributedKey string
}

type taskSpan struct {
	startMS int64
	endMS   int64
}

// BuildTaskDays 把请求合并成任务的活跃区间，再按统计日和工作时段拆分。
//
// 子会话通过 parent_session_id 归入根会话，与父会话属于同一任务；同一任务内并发请求的重叠时间只算一次，
// 不同任务并行时各自计算。相邻请求的空档不超过 idle 时连成一段，超过时断开且空档不计入。
// workStart / workEnd 是项目时区的分钟数，统计日从 workStart 开始，与用量报表的日期口径一致。
func BuildTaskDays(requests []TaskRequest, idle time.Duration, location *time.Location, workStart, workEnd int) []TaskDay {
	parents := make(map[string]string)
	for _, request := range requests {
		if request.SessionID != "" && request.ParentSessionID != "" {
			parents[request.SessionID] = request.ParentSessionID
		}
	}
	roots := TaskRoots(parents)
	ordered := append([]TaskRequest(nil), requests...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].StartMS < ordered[j].StartMS })
	accumulator := NewTaskAccumulator(idle, location, workStart, workEnd)
	for _, request := range ordered {
		if root, ok := roots[request.SessionID]; ok {
			request.SessionID = root
		}
		accumulator.Add(request)
	}
	if len(requests) == 0 {
		return nil
	}
	return accumulator.Days()
}

// addTaskSpan 在统计日边界和工作时段边界处切开活跃区间并累计。
//
// 标签算法与 repository.apiKeyTimeUsageQuery 的 SQL 一致：本地时刻 = epoch + 当时的 UTC 偏移，
// 统计日 = (本地秒 - workStart) / 86400，工作时段 = 本地秒 % 86400 落在 [workStart, workEnd)。
// 先在时区偏移变化处切开，段内偏移固定，边界都是整数运算，DST 切换日和午夜跳变的时区也与 Token 列对齐。
func addTaskSpan(days map[[2]int64]*TaskDay, apiKeyID int64, span taskSpan, location *time.Location, workStart, workEnd int) {
	const dayMS = 86400 * 1000
	startMS := span.startMS
	for startMS < span.endMS {
		at := time.UnixMilli(startMS).In(location)
		_, offset := at.Zone()
		pieceEnd := span.endMS
		if _, zoneEnd := at.ZoneBounds(); !zoneEnd.IsZero() && zoneEnd.UnixMilli() > startMS && zoneEnd.UnixMilli() < pieceEnd {
			pieceEnd = zoneEnd.UnixMilli()
		}
		shift := int64(offset) * 1000
		for startMS < pieceEnd {
			local := startMS + shift
			// 统计日起点之后的毫秒数；向下取整，兼容 1970 年之前的时刻。
			sinceDay := (local - int64(workStart)*60_000) % dayMS
			if sinceDay < 0 {
				sinceDay += dayMS
			}
			dayStartLocal := local - sinceDay
			statDay := (dayStartLocal - int64(workStart)*60_000) / dayMS
			workClose := dayStartLocal + int64(workEnd-workStart)*60_000
			work := local < workClose
			boundary := dayStartLocal + dayMS
			if work {
				boundary = workClose
			}
			boundary -= shift
			if boundary > pieceEnd {
				boundary = pieceEnd
			}
			key := [2]int64{apiKeyID, statDay}
			day := days[key]
			if day == nil {
				day = &TaskDay{APIKeyID: apiKeyID, StatDay: statDay}
				days[key] = day
			}
			if work {
				day.DaytimeMS += boundary - startMS
			} else {
				day.AfterHoursMS += boundary - startMS
			}
			startMS = boundary
		}
	}
}
