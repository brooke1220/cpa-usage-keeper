package apikeytime

import (
	"container/heap"
	"sort"
	"time"
)

// TaskRoots resolves the complete retained session graph before report filters
// are applied. Path compression handles long chains; cycles share one stable root.
func TaskRoots(parents map[string]string) map[string]string {
	roots := make(map[string]string, len(parents))
	positions := make(map[string]int)
	path := make([]string, 0, 16)
	for session := range parents {
		path = path[:0]
		root := session
		for {
			if resolved, ok := roots[root]; ok {
				root = resolved
				break
			}
			if index, ok := positions[root]; ok {
				for _, member := range path[index:] {
					if member < root {
						root = member
					}
				}
				break
			}
			parent := parents[root]
			if parent == "" || parent == root {
				break
			}
			positions[root] = len(path)
			path = append(path, root)
			root = parent
		}
		for _, member := range path {
			roots[member] = root
			delete(positions, member)
		}
		roots[session] = root
	}
	return roots
}

type pendingTask struct {
	taskSpan
	key   taskGroupKey
	index int
}

type taskHeap []*pendingTask

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].endMS < h[j].endMS }
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *taskHeap) Push(value any) {
	span := value.(*pendingTask)
	span.index = len(*h)
	*h = append(*h, span)
}
func (h *taskHeap) Pop() any {
	old := *h
	span := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return span
}

// TaskAccumulator consumes requests in start-time order. Only tasks within the
// idle horizon remain in memory; finished tasks are reduced to daily totals.
type TaskAccumulator struct {
	idleMS             int64
	location           *time.Location
	workStart, workEnd int
	active             map[taskGroupKey]*pendingTask
	pending            taskHeap
	spares             []*pendingTask
	days               map[[2]int64]*TaskDay
}

func NewTaskAccumulator(idle time.Duration, location *time.Location, workStart, workEnd int) *TaskAccumulator {
	if location == nil {
		location = time.Local
	}
	return &TaskAccumulator{
		idleMS: idle.Milliseconds(), location: location, workStart: workStart, workEnd: workEnd,
		active: make(map[taskGroupKey]*pendingTask), days: make(map[[2]int64]*TaskDay),
	}
}

// Add accepts an already resolved root in SessionID. Parent links are resolved
// outside the stream so an older or differently keyed ancestor is never lost.
func (a *TaskAccumulator) Add(request TaskRequest) {
	for len(a.pending) > 0 && request.StartMS-a.pending[0].endMS > a.idleMS {
		a.finish(heap.Pop(&a.pending).(*pendingTask))
	}
	key := taskGroupKey{apiKeyID: request.APIKeyID, root: request.SessionID}
	if request.APIKeyID == 0 {
		key.unattributedKey = request.APIGroupKey
	}
	if current := a.active[key]; current != nil {
		if request.EndMS > current.endMS {
			current.endMS = request.EndMS
			heap.Fix(&a.pending, current.index)
		}
		return
	}
	span := (*pendingTask)(nil)
	if len(a.spares) > 0 {
		span = a.spares[len(a.spares)-1]
		a.spares = a.spares[:len(a.spares)-1]
	} else {
		span = &pendingTask{}
	}
	*span = pendingTask{key: key, taskSpan: taskSpan{startMS: request.StartMS, endMS: max(request.StartMS, request.EndMS)}}
	a.active[key] = span
	heap.Push(&a.pending, span)
}

func (a *TaskAccumulator) finish(span *pendingTask) {
	addTaskSpan(a.days, span.key.apiKeyID, span.taskSpan, a.location, a.workStart, a.workEnd)
	delete(a.active, span.key)
	*span = pendingTask{}
	a.spares = append(a.spares, span)
}

// Days finishes the stream. Call it only after the last request.
func (a *TaskAccumulator) Days() []TaskDay {
	for len(a.pending) > 0 {
		a.finish(heap.Pop(&a.pending).(*pendingTask))
	}
	result := make([]TaskDay, 0, len(a.days))
	for _, day := range a.days {
		result = append(result, *day)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StatDay != result[j].StatDay {
			return result[i].StatDay < result[j].StatDay
		}
		return result[i].APIKeyID < result[j].APIKeyID
	})
	return result
}
