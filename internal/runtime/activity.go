package runtime

import (
	"strings"
	"sync"
	"time"

	"aiterm/internal/controller"
)

const maxRuntimeActivityItems = 200

type activityBuffer struct {
	mu       sync.Mutex
	snapshot controller.RuntimeActivitySnapshot
}

func newActivityBuffer() *activityBuffer {
	return &activityBuffer{}
}

func (b *activityBuffer) Start(owner controller.ConversationOwner, runtime string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	b.snapshot = controller.RuntimeActivitySnapshot{
		Owner:     owner,
		Active:    true,
		Runtime:   strings.TrimSpace(runtime),
		StartedAt: now,
		UpdatedAt: now,
		Items:     nil,
	}
}

func (b *activityBuffer) Publish(item controller.RuntimeActivityItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if item.Timestamp.IsZero() {
		item.Timestamp = time.Now().UTC()
	}
	b.snapshot.UpdatedAt = item.Timestamp
	if strings.TrimSpace(item.Runtime) != "" {
		b.snapshot.Runtime = strings.TrimSpace(item.Runtime)
	}
	if item.Replace && strings.TrimSpace(item.Key) != "" {
		for index := len(b.snapshot.Items) - 1; index >= 0; index-- {
			if b.snapshot.Items[index].Key == item.Key {
				b.snapshot.Items[index] = item
				return
			}
		}
	}
	b.snapshot.Items = append(b.snapshot.Items, item)
	if len(b.snapshot.Items) > maxRuntimeActivityItems {
		b.snapshot.Items = append([]controller.RuntimeActivityItem(nil), b.snapshot.Items[len(b.snapshot.Items)-maxRuntimeActivityItems:]...)
	}
}

func (b *activityBuffer) Finish(owner controller.ConversationOwner) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshot.Owner = owner
	b.snapshot.Active = false
	b.snapshot.UpdatedAt = time.Now().UTC()
}

func (b *activityBuffer) Snapshot() controller.RuntimeActivitySnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := b.snapshot
	snapshot.Items = append([]controller.RuntimeActivityItem(nil), b.snapshot.Items...)
	return snapshot
}
