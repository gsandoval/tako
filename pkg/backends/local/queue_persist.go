package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type persistedTask struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Queue      string    `json:"queue,omitempty"`
	Topic      string    `json:"topic,omitempty"`
	Subscriber string    `json:"subscriber,omitempty"`
	Payload    []byte    `json:"payload"`
	Attempt    int       `json:"attempt"`
	DueAt      time.Time `json:"due_at"`
	CreatedAt  time.Time `json:"created_at"`
}

func (d *QueueDispatcher) persistNewTask(task persistedTask) (*persistedTask, string) {
	if d.dir == "" {
		return &task, ""
	}
	seq := d.taskSeq.Add(1)
	task.ID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), seq)
	var dir string
	if task.Kind == "topic" {
		dir = filepath.Join(d.dir, "queues", "topic", task.Topic, task.Subscriber)
	} else {
		dir = filepath.Join(d.dir, "queues", "queue", task.Queue)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &task, ""
	}
	path := filepath.Join(dir, task.ID+".json")
	d.mu.Lock()
	d.activeTasks[path] = true
	d.mu.Unlock()
	d.writeTaskFile(path, task)
	return &task, path
}

func (d *QueueDispatcher) writeTaskFile(path string, task persistedTask) {
	if path == "" {
		return
	}
	raw, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err == nil {
		_ = os.Rename(tmp, path)
	}
}

func (d *QueueDispatcher) removeTask(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
	d.mu.Lock()
	delete(d.activeTasks, path)
	d.mu.Unlock()
}

func (d *QueueDispatcher) recoverQueueTasks(queue string, deliver func(context.Context, []byte) error) {
	dir := filepath.Join(d.dir, "queues", "queue", queue)
	to := delivery{source: queue, dlDir: filepath.Join("deadletter", queue)}
	d.recoverTasksInDir(dir, to, deliver)
}

func (d *QueueDispatcher) recoverTopicTasks(topic, subscriber string, deliver func(context.Context, []byte) error) {
	dir := filepath.Join(d.dir, "queues", "topic", topic, subscriber)
	to := delivery{
		source:   topic,
		consumer: subscriber,
		dlDir:    filepath.Join("deadletter", topic, subscriber),
	}
	d.recoverTasksInDir(dir, to, deliver)
}

func (d *QueueDispatcher) recoverTasksInDir(dir string, to delivery, deliver func(context.Context, []byte) error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		d.mu.Lock()
		if d.activeTasks[path] || d.closed {
			d.mu.Unlock()
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			d.mu.Unlock()
			continue
		}
		var task persistedTask
		if err := json.Unmarshal(raw, &task); err != nil {
			d.mu.Unlock()
			continue
		}
		d.activeTasks[path] = true
		stopCh := d.stopCh
		d.mu.Unlock()

		taskCopy := task
		wait := time.Until(taskCopy.DueAt)
		if wait <= 0 {
			d.dispatch(to, deliver, taskCopy.Payload, &taskCopy, path)
		} else {
			go func(t persistedTask, p string, delay time.Duration) {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
					d.dispatch(to, deliver, t.Payload, &t, p)
				case <-stopCh:
				}
			}(taskCopy, path, wait)
		}
	}
}
