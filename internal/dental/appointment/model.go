package appointment

import (
	"fmt"
	"strconv"

	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// 预约状态机：booked(预约) → done(完成，签到即完成)；分支 noshow(爽约) / cancel(取消)。
const (
	Booked = "booked"
	Done   = "done"
	NoShow = "noshow"
	Cancel = "cancel"
)

// Appointment 预约（按医生/时段排）。
type Appointment struct {
	model.Doc   `bson:",inline"`
	PatientID   bson.ObjectID `bson:"patient_id"`
	PatientName string        `bson:"patient_name"` // 快照，免 join
	DoctorID    bson.ObjectID `bson:"doctor_id,omitempty"`
	Doctor      string        `bson:"doctor"` // 接诊医生快照（签到时最终确定）
	Date        string        `bson:"date"` // YYYY-MM-DD
	Slot        string        `bson:"slot"` // HH:MM
	Status      string        `bson:"status"`
}

// 放号档位默认值：半小时一档，每档 1 人（租户在门诊设置改）。
const (
	DefaultSlotMinutes  = 30
	DefaultSlotCapacity = 1
)

// Slot 放号档位选项：Value 下单用（桶起点 HH:MM），Label 展示用（09:00-09:30）。
type Slot struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// DaySlots 当天放号档位（8:00 起约，20:00 为最晚起约，非法粒度回落默认）。
func DaySlots(minutes int) []Slot {
	if minutes != 15 && minutes != 30 && minutes != 60 {
		minutes = DefaultSlotMinutes
	}
	var out []Slot
	for start := 8 * 60; start <= 20*60; start += minutes {
		end := start + minutes
		out = append(out, Slot{
			Value: fmt.Sprintf("%02d:%02d", start/60, start%60),
			Label: fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60),
		})
	}
	return out
}

// NormSlotConfig 下单配置归一（0/非法回落默认，防脏调用）。
func NormSlotConfig(minutes, capacity int) (int, int) {
	if minutes != 15 && minutes != 30 && minutes != 60 {
		minutes = DefaultSlotMinutes
	}
	if capacity < 1 || capacity > 10 {
		capacity = DefaultSlotCapacity
	}
	return minutes, capacity
}

// statusMeta 状态元数据（State 模式的数据驱动版）：中文名与徽章色收敛到一张表，
// 加状态只改这里，模板/流转天然同步，不会出现"名对了色错了"。
type statusMeta struct{ Name, Badge string }

var statusMetas = map[string]statusMeta{
	Booked:  {"已预约", "bg-primary"},
	Done:    {"已完成", "bg-success"},
	NoShow:  {"爽约", "bg-warning text-dark"},
	Cancel:  {"已取消", "bg-secondary"},
}

// StatusName 状态中文名（模板调用）。
func (a Appointment) StatusName() string {
	if m, ok := statusMetas[a.Status]; ok {
		return m.Name
	}
	return a.Status
}

// Badge Bootstrap 颜色类（模板调用）。
func (a Appointment) Badge() string {
	if m, ok := statusMetas[a.Status]; ok {
		return m.Badge
	}
	return "bg-secondary"
}

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}
