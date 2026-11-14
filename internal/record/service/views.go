package service

import (
	"fmt"
	"time"

	"clinic-backend/internal/database"
	recordmodel "clinic-backend/internal/record/model"
	taggingservice "clinic-backend/internal/tagging/service"
)

func (s *AdminRecordService) buildView(rec recordmodel.ClinicRecord) (AdminRecordView, error) {
	return s.buildViewTx(s.db, rec)
}

// recordViewRelations holds the room names, optional side-table rows, and
// photo counts for a batch of records, keyed by record ID, so views can be
// assembled from a fixed number of queries instead of probing each table once
// per record.
type recordViewRelations struct {
	rooms       map[uint]string
	devices     map[uint]recordmodel.ClinicRecordDevice
	workers     map[uint]recordmodel.ClinicRecordWorker
	staffNames  map[int]string
	photoCounts map[uint]int64
	rejections  map[uint]recordmodel.ClinicRecordRejection
	referrals   map[uint]recordmodel.ClinicRecordReferral
	arrivals    map[uint]recordmodel.ClinicRecordArrival
}

// loadRecordViewRelations loads the room name and the optional side-table rows
// for every record in recs using one query per table.
func loadRecordViewRelations(db rowsQueryer, recs []recordmodel.ClinicRecord) (recordViewRelations, error) {
	rel := recordViewRelations{
		rooms:       make(map[uint]string),
		devices:     make(map[uint]recordmodel.ClinicRecordDevice),
		workers:     make(map[uint]recordmodel.ClinicRecordWorker),
		staffNames:  make(map[int]string),
		photoCounts: make(map[uint]int64),
		rejections:  make(map[uint]recordmodel.ClinicRecordRejection),
		referrals:   make(map[uint]recordmodel.ClinicRecordReferral),
		arrivals:    make(map[uint]recordmodel.ClinicRecordArrival),
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
	idArgs := uintArgs(ids)

	if len(roomIDs) > 0 {
		rows, err := db.Query(
			"SELECT id, name FROM clinic_room WHERE id IN ("+database.Placeholders(1, len(roomIDs))+")",
			uintArgs(roomIDs)...,
		)
		if err != nil {
			return rel, fmt.Errorf("load rooms: %w", err)
		}
		for rows.Next() {
			var id uint
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				rows.Close()
				return rel, fmt.Errorf("scan room: %w", err)
			}
			rel.rooms[id] = name
		}
		if err := rows.Close(); err != nil {
			return rel, fmt.Errorf("load rooms: %w", err)
		}
		if err := rows.Err(); err != nil {
			return rel, fmt.Errorf("load rooms: %w", err)
		}
	}

	rows, err := db.Query(
		"SELECT "+recordDeviceColumns+" FROM clinic_record_device WHERE record_id IN ("+database.Placeholders(1, len(ids))+")",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load devices: %w", err)
	}
	for rows.Next() {
		device, err := scanRecordDevice(rows)
		if err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan device: %w", err)
		}
		rel.devices[device.RecordID] = device
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load devices: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load devices: %w", err)
	}

	rows, err = db.Query(
		"SELECT "+recordWorkerColumns+" FROM clinic_record_worker WHERE record_id IN ("+database.Placeholders(1, len(ids))+")",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load workers: %w", err)
	}
	staffIDs := make([]int, 0, len(recs))
	seenStaff := make(map[int]struct{}, len(recs))
	for _, rec := range recs {
		if rec.ApproverID == nil {
			continue
		}
		id := int(*rec.ApproverID)
		if _, ok := seenStaff[id]; ok {
			continue
		}
		seenStaff[id] = struct{}{}
		staffIDs = append(staffIDs, id)
	}
	for rows.Next() {
		worker, err := scanRecordWorker(rows)
		if err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan worker: %w", err)
		}
		rel.workers[worker.RecordID] = worker
		id := int(worker.WorkerID)
		if _, ok := seenStaff[id]; ok {
			continue
		}
		seenStaff[id] = struct{}{}
		staffIDs = append(staffIDs, id)
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load workers: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load workers: %w", err)
	}

	if len(staffIDs) > 0 {
		staffArgs := make([]any, len(staffIDs))
		for i, id := range staffIDs {
			staffArgs[i] = id
		}
		rows, err := db.Query(
			"SELECT id, realname FROM clinic_staff WHERE id IN ("+database.Placeholders(1, len(staffIDs))+")",
			staffArgs...,
		)
		if err != nil {
			return rel, fmt.Errorf("load staff names: %w", err)
		}
		for rows.Next() {
			var id int
			var realname string
			if err := rows.Scan(&id, &realname); err != nil {
				rows.Close()
				return rel, fmt.Errorf("scan staff name: %w", err)
			}
			rel.staffNames[id] = realname
		}
		if err := rows.Close(); err != nil {
			return rel, fmt.Errorf("load staff names: %w", err)
		}
		if err := rows.Err(); err != nil {
			return rel, fmt.Errorf("load staff names: %w", err)
		}
	}

	rows, err = db.Query(
		"SELECT record_id, COUNT(*) FROM clinic_record_photo WHERE record_id IN ("+database.Placeholders(1, len(ids))+") GROUP BY record_id",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load photo counts: %w", err)
	}
	for rows.Next() {
		var recordID uint
		var count int64
		if err := rows.Scan(&recordID, &count); err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan photo count: %w", err)
		}
		rel.photoCounts[recordID] = count
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load photo counts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load photo counts: %w", err)
	}

	rows, err = db.Query(
		"SELECT "+recordRejectionColumns+" FROM clinic_record_rejection WHERE record_id IN ("+database.Placeholders(1, len(ids))+")",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load rejections: %w", err)
	}
	for rows.Next() {
		rejection, err := scanRecordRejection(rows)
		if err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan rejection: %w", err)
		}
		rel.rejections[rejection.RecordID] = rejection
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load rejections: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load rejections: %w", err)
	}

	rows, err = db.Query(
		"SELECT "+recordReferralColumns+" FROM clinic_record_referral WHERE record_id IN ("+database.Placeholders(1, len(ids))+")",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load referrals: %w", err)
	}
	for rows.Next() {
		referral, err := scanRecordReferral(rows)
		if err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan referral: %w", err)
		}
		rel.referrals[referral.RecordID] = referral
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load referrals: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load referrals: %w", err)
	}

	rows, err = db.Query(
		"SELECT "+recordArrivalColumns+" FROM clinic_record_arrival WHERE record_id IN ("+database.Placeholders(1, len(ids))+")",
		idArgs...,
	)
	if err != nil {
		return rel, fmt.Errorf("load arrivals: %w", err)
	}
	for rows.Next() {
		arrival, err := scanRecordArrival(rows)
		if err != nil {
			rows.Close()
			return rel, fmt.Errorf("scan arrival: %w", err)
		}
		rel.arrivals[arrival.RecordID] = arrival
	}
	if err := rows.Close(); err != nil {
		return rel, fmt.Errorf("load arrivals: %w", err)
	}
	if err := rows.Err(); err != nil {
		return rel, fmt.Errorf("load arrivals: %w", err)
	}

	return rel, nil
}

func uintArgs(ids []uint) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// buildViewsTx assembles the admin view for each record using a single batch of
// relation loads. The returned slice follows the same order as recs.
func (s *AdminRecordService) buildViewsTx(db rowsQueryer, recs []recordmodel.ClinicRecord) ([]AdminRecordView, error) {
	rel, err := loadRecordViewRelations(db, recs)
	if err != nil {
		return nil, err
	}
	views := make([]AdminRecordView, 0, len(recs))
	for _, rec := range recs {
		views = append(views, s.assembleView(rec, rel))
	}
	return views, nil
}

func (s *AdminRecordService) buildViewTx(db rowsQueryer, rec recordmodel.ClinicRecord) (AdminRecordView, error) {
	views, err := s.buildViewsTx(db, []recordmodel.ClinicRecord{rec})
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
		PhotoCount:        rel.photoCounts[rec.ID],
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
	if rec.ApproverID != nil {
		v.ApproverRealname = rel.staffNames[int(*rec.ApproverID)]
	}
	if worker, ok := rel.workers[rec.ID]; ok {
		v.WorkerDesc = worker.WorkerDesc
		v.WorkerID = &worker.WorkerID
		v.WorkerRealname = rel.staffNames[int(worker.WorkerID)]
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
