package test

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"cpa-usage-keeper/internal/apikeytime"
)

var taskLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

func TestTaskRootsResolvesLongChainsAndCycles(t *testing.T) {
	parents := make(map[string]string)
	for i := 1; i <= 100; i++ {
		parents["session-"+strconv.Itoa(i)] = "session-" + strconv.Itoa(i-1)
	}
	parents["cycle-b"], parents["cycle-c"], parents["cycle-a"] = "cycle-c", "cycle-a", "cycle-b"
	parents["cycle-child"] = "cycle-c"
	for pass := 0; pass < 10; pass++ {
		roots := apikeytime.TaskRoots(parents)
		if roots["session-100"] != "session-0" {
			t.Fatalf("long chain truncated: %s", roots["session-100"])
		}
		for _, session := range []string{"cycle-a", "cycle-b", "cycle-c", "cycle-child"} {
			if roots[session] != "cycle-a" {
				t.Fatalf("unstable cycle root for %s: %s", session, roots[session])
			}
		}
	}
}

const (
	taskWorkStart = 9 * 60
	taskWorkEnd   = 18 * 60
)

func taskMS(day, hour, minute int) int64 {
	return time.Date(2026, 10, day, hour, minute, 0, 0, taskLocation).UnixMilli()
}

func taskStatDay(day int) int64 {
	return time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func buildTaskDays(requests []apikeytime.TaskRequest, idleMinutes int) []apikeytime.TaskDay {
	return apikeytime.BuildTaskDays(requests, time.Duration(idleMinutes)*time.Minute, taskLocation, taskWorkStart, taskWorkEnd)
}

func TestBuildTaskDaysBridgesShortGapsAndDropsLongGaps(t *testing.T) {
	requests := []apikeytime.TaskRequest{
		{APIKeyID: 1, SessionID: "s1", StartMS: taskMS(8, 10, 0), EndMS: taskMS(8, 10, 0) + 60_000},
		// A 4-minute gap is local work inside the same task.
		{APIKeyID: 1, SessionID: "s1", StartMS: taskMS(8, 10, 5), EndMS: taskMS(8, 10, 5) + 30_000},
		// A 24.5-minute gap is a break under the default threshold.
		{APIKeyID: 1, SessionID: "s1", StartMS: taskMS(8, 10, 30), EndMS: taskMS(8, 10, 30) + 60_000},
	}
	if got, want := buildTaskDays(requests, 10), []apikeytime.TaskDay{{APIKeyID: 1, StatDay: taskStatDay(8), DaytimeMS: 330_000 + 60_000}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("10-minute idle: got %+v want %+v", got, want)
	}
	if got, want := buildTaskDays(requests, 30), []apikeytime.TaskDay{{APIKeyID: 1, StatDay: taskStatDay(8), DaytimeMS: 31 * 60_000}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("30-minute idle: got %+v want %+v", got, want)
	}
}

func TestBuildTaskDaysIdleThresholdIsInclusive(t *testing.T) {
	end := taskMS(8, 10, 1)
	exact := []apikeytime.TaskRequest{
		{APIKeyID: 1, SessionID: "s1", StartMS: taskMS(8, 10, 0), EndMS: end},
		{APIKeyID: 1, SessionID: "s1", StartMS: end + 10*60_000, EndMS: end + 11*60_000},
	}
	if got := buildTaskDays(exact, 10); len(got) != 1 || got[0].DaytimeMS != 12*60_000 {
		t.Fatalf("a gap equal to the threshold should bridge: %+v", got)
	}
	exact[1].StartMS++
	exact[1].EndMS++
	if got := buildTaskDays(exact, 10); len(got) != 1 || got[0].DaytimeMS != 2*60_000 {
		t.Fatalf("a gap 1 ms over the threshold should break: %+v", got)
	}
}

// Cuba springs forward at local midnight, so the statistical-day boundary itself falls in the skipped hour.
func TestBuildTaskDaysSplitsLongSpansAcrossMidnightDSTJump(t *testing.T) {
	havana, err := time.LoadLocation("America/Havana")
	if err != nil {
		t.Fatalf("load America/Havana: %v", err)
	}
	start := time.Date(2026, 3, 7, 12, 0, 0, 0, havana).UnixMilli()
	requests := []apikeytime.TaskRequest{{APIKeyID: 1, SessionID: "long", StartMS: start, EndMS: start + 72*3_600_000}}
	got := apikeytime.BuildTaskDays(requests, 10*time.Minute, havana, 0, 24*60)
	statDay := func(day int) int64 { return time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Unix() / 86400 }
	want := []apikeytime.TaskDay{
		{APIKeyID: 1, StatDay: statDay(7), DaytimeMS: 12 * 3_600_000},
		{APIKeyID: 1, StatDay: statDay(8), DaytimeMS: 23 * 3_600_000},
		{APIKeyID: 1, StatDay: statDay(9), DaytimeMS: 24 * 3_600_000},
		{APIKeyID: 1, StatDay: statDay(10), DaytimeMS: 13 * 3_600_000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildTaskDaysMergesSubagentsAndAddsParallelTasks(t *testing.T) {
	requests := []apikeytime.TaskRequest{
		// The grandchild comes first to show root resolution does not depend on input order.
		{APIKeyID: 1, SessionID: "g1", ParentSessionID: "c1", StartMS: taskMS(8, 10, 12), EndMS: taskMS(8, 10, 13)},
		{APIKeyID: 1, SessionID: "s1", StartMS: taskMS(8, 10, 0), EndMS: taskMS(8, 10, 10)},
		{APIKeyID: 1, SessionID: "c1", ParentSessionID: "s1", StartMS: taskMS(8, 10, 2), EndMS: taskMS(8, 10, 5)},
		// An independent session running at the same time is a separate task.
		{APIKeyID: 1, SessionID: "s2", StartMS: taskMS(8, 10, 0), EndMS: taskMS(8, 10, 5)},
	}
	want := []apikeytime.TaskDay{{APIKeyID: 1, StatDay: taskStatDay(8), DaytimeMS: (13 + 5) * 60_000}}
	if got := buildTaskDays(requests, 10); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildTaskDaysSplitsAtWorkEndAndStatisticalDay(t *testing.T) {
	requests := []apikeytime.TaskRequest{
		{APIKeyID: 1, SessionID: "evening", StartMS: taskMS(8, 17, 50), EndMS: taskMS(8, 18, 10)},
		// Before 09:00 still belongs to the previous statistical day.
		{APIKeyID: 1, SessionID: "morning", StartMS: taskMS(9, 8, 50), EndMS: taskMS(9, 9, 10)},
	}
	// Day 8 off-hours: 18:00–18:10 plus the next morning's 08:50–09:00.
	want := []apikeytime.TaskDay{
		{APIKeyID: 1, StatDay: taskStatDay(8), DaytimeMS: 10 * 60_000, AfterHoursMS: 20 * 60_000},
		{APIKeyID: 1, StatDay: taskStatDay(9), DaytimeMS: 10 * 60_000},
	}
	if got := buildTaskDays(requests, 10); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestBuildTaskDaysGroupsSessionlessRequestsByKey(t *testing.T) {
	if got := buildTaskDays(nil, 10); got != nil {
		t.Fatalf("empty input: got %+v", got)
	}
	requests := []apikeytime.TaskRequest{
		{APIKeyID: 1, StartMS: taskMS(8, 10, 0), EndMS: taskMS(8, 10, 1)},
		{APIKeyID: 1, StartMS: taskMS(8, 10, 3), EndMS: taskMS(8, 10, 4)},
		{APIKeyID: 2, StartMS: taskMS(8, 10, 0), EndMS: taskMS(8, 10, 1)},
	}
	want := []apikeytime.TaskDay{
		{APIKeyID: 1, StatDay: taskStatDay(8), DaytimeMS: 4 * 60_000},
		{APIKeyID: 2, StatDay: taskStatDay(8), DaytimeMS: 60_000},
	}
	if got := buildTaskDays(requests, 10); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
