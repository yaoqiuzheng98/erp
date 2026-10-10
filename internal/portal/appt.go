package portal

import (
	"erp/internal/dental/appointment"
)

// visibleAppts 患者端预约只看待签到（booked）。
func visibleAppts(list []appointment.Appointment) []appointment.Appointment {
	out := make([]appointment.Appointment, 0, len(list))
	for _, a := range list {
		if a.Status == appointment.Booked {
			out = append(out, a)
		}
	}
	return out
}
