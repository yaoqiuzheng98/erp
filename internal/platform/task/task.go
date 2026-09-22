package task

import (
	"context"
	"log/slog"
	"time"

	gocron "github.com/go-co-op/gocron/v2"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// TaskDef 插件声明的周期任务；Run 在注册时闭包捕获 Env。
type TaskDef struct {
	Name      string
	Cron      string        // 5 字段 cron 表达式
	Every     time.Duration // 或固定间隔
	Singleton bool
	Run       func(ctx context.Context) error
}

// Scheduler 包装 gocron.Scheduler，按插件 ID 打标签管理，执行记录落 task_runs。
type Scheduler struct {
	s   gocron.Scheduler
	col *mongo.Collection
}

func New(db *mongo.Database) (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, err
	}
	return &Scheduler{s: s, col: db.Collection("task_runs")}, nil
}

func (sc *Scheduler) record(name string, start time.Time, runErr error) {
	status := "ok"
	msg := ""
	if runErr != nil {
		status = "error"
		msg = runErr.Error()
	}
	_, _ = sc.col.InsertOne(context.Background(), bson.M{
		"job_name":    name,
		"started_at":  start,
		"finished_at": time.Now(),
		"status":      status,
		"error":       msg,
	})
}

// RegisterPlugin 把插件任务注册为 gocron job，标签为插件 ID。
func (sc *Scheduler) RegisterPlugin(pluginID string, defs []TaskDef) {
	for _, d := range defs {
		var jobDef gocron.JobDefinition
		switch {
		case d.Cron != "":
			jobDef = gocron.CronJob(d.Cron, false)
		case d.Every > 0:
			jobDef = gocron.DurationJob(d.Every)
		default:
			continue
		}
		name := pluginID + "/" + d.Name
		run := d.Run
		opts := []gocron.JobOption{
			gocron.WithName(name),
			gocron.WithTags(pluginID),
		}
		if d.Singleton {
			opts = append(opts, gocron.WithSingletonMode(gocron.LimitModeReschedule))
		}
		task := gocron.NewTask(func() {
			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := run(ctx); err != nil {
				slog.Error("task failed", "job", name, "err", err)
				sc.record(name, start, err)
				return
			}
			sc.record(name, start, nil)
		})
		if _, err := sc.s.NewJob(jobDef, task, opts...); err != nil {
			slog.Error("register task failed", "job", name, "err", err)
		}
	}
}

// RemovePlugin 停掉某插件全部任务。
func (sc *Scheduler) RemovePlugin(pluginID string) {
	sc.s.RemoveByTags(pluginID)
}

func (sc *Scheduler) Start() { sc.s.Start() }

func (sc *Scheduler) Shutdown() { _ = sc.s.Shutdown() }
