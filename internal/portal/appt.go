package portal

import (
	"erp/internal/dental/appointment"
)

// visibleAppts 患者端预约只看前半段：待签到(booked) + 候诊(arrived，含排位)；
// 进诊室后预约单退场，后续看账单。门诊后台病历不受影响（直接调 OfPatient）。
func visibleAppts(list []appointment.Appointment) []appointment.Appointment {
	out := make([]appointment.Appointment, 0, len(list))
	for _, a := range list {
		if a.Status == appointment.Booked || a.Status == appointment.Arrived {
			out = append(out, a)
		}
	}
	return out
}
