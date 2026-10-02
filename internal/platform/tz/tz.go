// Package tz 北京时间：业务日期边界统一走东八区，不随容器 TZ 漂移。
// 中国无夏令时，用 FixedZone 即可，不依赖系统 tzdata。
package tz

import "time"

var shanghai = time.FixedZone("CST", 8*3600)

// Now 当前北京时间。
func Now() time.Time { return time.Now().In(shanghai) }

// Today 今天（YYYY-MM-DD）：预约默认日期、今日列表都走这里。
func Today() string { return Now().Format("2006-01-02") }

// MonthKey 当月（YYYYMM）：单据编号年月段走这里。
func MonthKey() string { return Now().Format("200601") }
