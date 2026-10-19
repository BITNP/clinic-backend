package model

import (
	"time"
)

// ClinicRecordWorker records the staff assignment and completion notes for a repair record.
type ClinicRecordWorker struct {
	RecordID   uint      `gorm:"primaryKey;not null;column:record_id" json:"record_id"`
	WorkerID   uint      `gorm:"not null;column:worker" json:"worker"`
	WorkerDesc string    `gorm:"size:10000;column:worker_desc" json:"worker_desc"`
	FinishTime time.Time `gorm:"column:finish_time" json:"finish_time"`
}

// TableName overrides GORM's default pluralized table name.
func (ClinicRecordWorker) TableName() string {
	return "clinic_record_worker"
}
