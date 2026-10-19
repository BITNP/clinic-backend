package service

import (
	"fmt"
	"time"

	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	taggingservice "clinic-backend/internal/tagging/service"

	"gorm.io/gorm"
)

func (s *AdminRecordService) buildView(rec recordmodel.ClinicRecord) (AdminRecordView, error) {
	return s.buildViewTx(s.db, rec)
}

// recordViewRelations holds the room names and optional side-table rows for a
// batch of records, keyed by record ID, so views can be assembled from a fixed
// number of queries instead of probing each table once per record.
type recordViewRelations struct {
	rooms      map[uint]string
	devices    map[uint]recordmodel.ClinicRecordDevice
	workers    map[uint]recordmodel.ClinicRecordWorker
	rejections map[uint]recordmodel.ClinicRecordRejection
	referrals  map[uint]recordmodel.ClinicRecordReferral
	arrivals   map[uint]recordmodel.ClinicRecordArrival
}

// loadRecordViewRelationsTx loads the room name and the optional side-table rows
// for every record in recs using one query per table.
func loadRecordViewRelationsTx(tx *gorm.DB, recs []recordmodel.ClinicRecord) (recordViewRelations, error) {
	rel := recordViewRelations{
		rooms:      make(map[uint]string),
		devices:    make(map[uint]recordmodel.ClinicRecordDevice),
		workers:    make(map[uint]recordmodel.ClinicRecordWorker),
		rejections: make(map[uint]recordmodel.ClinicRecordRejection),
		referrals:  make(map[uint]recordmodel.ClinicRecordReferral),
		arrivals:   make(map[uint]recordmodel.ClinicRecordArrival),
	}
	if len(recs) == 0 {
		return rel, nil
	}

	ids := make([]uint, 0, len(recs))
	roomIDs := make([]uint, 0, len(recs))
	seenRoom := make(map[uint]struct{}, len(recs))
	for _, rec := range recs {
		ids = append(ids, rec.ID)
		if rec.RoomID != 0 {
			if _, ok := seenRoom[rec.RoomID]; !ok {
				seenRoom[rec.RoomID] = struct{}{}
				roomIDs = append(roomIDs, rec.RoomID)
			}
		}
	}

	if len(roomIDs) > 0 {
		var rooms []roommodel.ClinicRoom
		if err := tx.Select("id", "name").Where("id IN ?", roomIDs).Find(&rooms).Error; err != nil {
			return rel, fmt.Errorf("load rooms: %w", err)
		}
		for _, r := range rooms {
			rel.rooms[r.ID] = r.Name
		}
	}

	var devices []recordmodel.ClinicRecordDevice
	if err := tx.Where("record_id IN ?", ids).Find(&devices).Error; err != nil {
		return rel, fmt.Errorf("load devices: %w", err)
	}
	for _, d := range devices {
		rel.devices[d.RecordID] = d
	}

	var workers []recordmodel.ClinicRecordWorker
	if err := tx.Where("record_id IN ?", ids).Find(&workers).Error; err != nil {
		return rel, fmt.Errorf("load workers: %w", err)
	}
	for _, w := range workers {
		rel.workers[w.RecordID] = w
	}

	var rejections []recordmodel.ClinicRecordRejection
	if err := tx.Where("record_id IN ?", ids).Find(&rejections).Error; err != nil {
		return rel, fmt.Errorf("load rejections: %w", err)
	}
	for _, r := range rejections {
		rel.rejections[r.RecordID] = r
	}

	var referrals []recordmodel.ClinicRecordReferral
	if err := tx.Where("record_id IN ?", ids).Find(&referrals).Error; err != nil {
		return rel, fmt.Errorf("load referrals: %w", err)
	}
	for _, r := range referrals {
		rel.referrals[r.RecordID] = r
	}

	var arrivals []recordmodel.ClinicRecordArrival
	if err := tx.Where("record_id IN ?", ids).Find(&arrivals).Error; err != nil {
		return rel, fmt.Errorf("load arrivals: %w", err)
	}
	for _, a := range arrivals {
		rel.arrivals[a.RecordID] = a
	}

	return rel, nil
}

// buildViewsTx assembles the admin view for each record using a single batch of
// relation loads. The returned slice follows the same order as recs.
func (s *AdminRecordService) buildViewsTx(tx *gorm.DB, recs []recordmodel.ClinicRecord) ([]AdminRecordView, error) {
	rel, err := loadRecordViewRelationsTx(tx, recs)
	if err != nil {
		return nil, err
	}
	views := make([]AdminRecordView, 0, len(recs))
	for _, rec := range recs {
		views = append(views, s.assembleView(rec, rel))
	}
	return views, nil
}

func (s *AdminRecordService) buildViewTx(tx *gorm.DB, rec recordmodel.ClinicRecord) (AdminRecordView, error) {
	views, err := s.buildViewsTx(tx, []recordmodel.ClinicRecord{rec})
	if err != nil {
		return AdminRecordView{}, err
	}
	return views[0], nil
}

// assembleView maps a record and its preloaded relations onto the view.
func (s *AdminRecordService) assembleView(rec recordmodel.ClinicRecord, rel recordViewRelations) AdminRecordView {
	v := AdminRecordView{
		ID:                rec.ID,
		User:              rec.User,
		Realname:          rec.Realname,
		PhoneNum:          rec.PhoneNum,
		Status:            string(rec.Status),
		AppointmentTime:   rec.AppointmentTime.UTC().Format("2006-01-02"),
		Description:       rec.QuestionDesc,
		Campus:            rel.rooms[rec.RoomID],
		ApproverID:        rec.ApproverID,
		LiquidMetalStatus: string(taggingservice.LiquidMetalStatusFromIndex(rec.LiquidMetalStatusIndex)),
	}
	if s.tagSvc != nil {
		if tag, ok := s.tagSvc.ByID(rec.TagID); ok {
			v.Tag = tag.Title
		}
	}
	if device, ok := rel.devices[rec.ID]; ok {
		v.Model = device.LaptopModel
		v.Password = device.Password
	}
	if worker, ok := rel.workers[rec.ID]; ok {
		v.WorkerDesc = worker.WorkerDesc
		v.WorkerID = &worker.WorkerID
		if !worker.FinishTime.IsZero() {
			v.FinishTime = new(worker.FinishTime.UTC().Format(time.RFC3339))
		}
	}
	if rejection, ok := rel.rejections[rec.ID]; ok {
		v.RejectReason = rejection.RejectReason
	}
	if referral, ok := rel.referrals[rec.ID]; ok {
		v.ReferralReason = referral.ReferralReason
	}
	if arrival, ok := rel.arrivals[rec.ID]; ok {
		v.ArriveTime = new(arrival.ArriveTime.UTC().Format(time.RFC3339))
	}
	return v
}
