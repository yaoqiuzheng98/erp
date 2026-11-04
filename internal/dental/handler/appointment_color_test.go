package handler

import (
	"testing"

	"erp/internal/dental/appointment"
	"erp/internal/platform/auth"
	"erp/internal/platform/model"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestApptColors 同医生同色；按可接诊医生顺序取色；不在册/老单有兜底。
func TestApptColors(t *testing.T) {
	id1, id2 := bson.NewObjectID(), bson.NewObjectID()
	docs := []auth.User{{ID: id1, Name: "医生甲"}, {ID: id2, Name: "医生乙"}}
	a1 := appointment.Appointment{Doc: model.Doc{ID: bson.NewObjectID()}, DoctorID: id2}
	a2 := appointment.Appointment{Doc: model.Doc{ID: bson.NewObjectID()}, DoctorID: id2}
	a3 := appointment.Appointment{Doc: model.Doc{ID: bson.NewObjectID()}, Doctor: "医生甲"} // 老单没医生 ID，按姓名兜底
	a4 := appointment.Appointment{Doc: model.Doc{ID: bson.NewObjectID()}, DoctorID: bson.NewObjectID(), Doctor: "离职医生"}

	got := apptColors([]appointment.Appointment{a1, a2, a3, a4}, docs)
	if got[a1.ID.Hex()] != "doc-bg-1" || got[a2.ID.Hex()] != "doc-bg-1" {
		t.Fatalf("same doctor colors = %q/%q, want doc-bg-1", got[a1.ID.Hex()], got[a2.ID.Hex()])
	}
	if got[a3.ID.Hex()] != "doc-bg-0" {
		t.Fatalf("legacy name color = %q, want doc-bg-0", got[a3.ID.Hex()])
	}
	if got[a4.ID.Hex()] != "doc-bg-unknown" {
		t.Fatalf("unknown doctor color = %q, want doc-bg-unknown", got[a4.ID.Hex()])
	}
}
