package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"clinic-backend/internal/calendar"
	recordmodel "clinic-backend/internal/record/model"
	roommodel "clinic-backend/internal/room/model"
	roomservice "clinic-backend/internal/room/service"
	schedulingmodel "clinic-backend/internal/scheduling/model"
)

func TestPreferredRoom(t *testing.T) {
	today := calendar.DateInLocation(time.Now(), calendar.DefaultServiceDateLocation())
	for _, tc := range []struct {
		name       string
		policy     roommodel.SelectionPolicy
		counts     [2]int
		capacities [2]uint
		want       int // 0 means no recommendation; otherwise room index + 1
	}{
		{"balanced", roommodel.SelectionPolicyBalanced, [2]int{7, 2}, [2]uint{10, 10}, 2},
		{"fulfill", roommodel.SelectionPolicyFulfill, [2]int{7, 2}, [2]uint{10, 10}, 1},
		{"balanced tie", roommodel.SelectionPolicyBalanced, [2]int{2, 2}, [2]uint{10, 10}, 1},
		{"fulfill advances", roommodel.SelectionPolicyFulfill, [2]int{10, 2}, [2]uint{10, 10}, 2},
		{"balanced skips full", roommodel.SelectionPolicyBalanced, [2]int{1, 2}, [2]uint{1, 10}, 2},
		{"all full", roommodel.SelectionPolicyBalanced, [2]int{1, 1}, [2]uint{1, 1}, 0},
		{"zero capacity", roommodel.SelectionPolicyFulfill, [2]int{}, [2]uint{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupRoomServiceDB(t)
			campus := roommodel.ClinicCampus{Name: "campus", SelectionPolicy: tc.policy}
			if err := db.Create(&campus).Error; err != nil {
				t.Fatal(err)
			}
			var ids []uint
			for i, name := range []string{"A", "B"} {
				room := roommodel.ClinicRoom{Name: name, Enabled: true, CampusID: &campus.ID}
				if err := db.Create(&room).Error; err != nil {
					t.Fatal(err)
				}
				ids = append(ids, room.ID)
				d := schedulingmodel.ClinicServiceDate{RoomID: &room.ID, Date: today, Capacity: tc.capacities[i]}
				if err := db.Create(&d).Error; err != nil {
					t.Fatal(err)
				}
				for n := 0; n < tc.counts[i]; n++ {
					if err := db.Create(&recordmodel.ClinicRecord{RoomID: room.ID, AppointmentTime: today, Status: recordmodel.RecordStatusPending}).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := roomservice.NewRoomService(db).PreferredRoom(context.Background(), campus, today, roomservice.ListRoomFilter{Page: 99, PageSize: 1})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == 0 {
				if got != nil {
					t.Fatalf("want nil got %d", *got)
				}
				return
			}
			if got == nil || *got != ids[tc.want-1] {
				t.Fatalf("got %v want room %d", got, ids[tc.want-1])
			}
		})
	}
}

func TestPreferredRoomEligibilityAndCounts(t *testing.T) {
	today := calendar.DateInLocation(time.Now(), calendar.DefaultServiceDateLocation())
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "today", true: "future"}[future], func(t *testing.T) {
			db := setupRoomServiceDB(t)
			svc := roomservice.NewRoomService(db)
			campus := roommodel.ClinicCampus{Name: "main"}
			other := roommodel.ClinicCampus{Name: "other"}
			if err := db.Create(&campus).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&other).Error; err != nil {
				t.Fatal(err)
			}
			date := today
			if future {
				date = date.AddDate(0, 0, 1)
			}
			var target uint
			for i, name := range []string{"disabled", "closed", "other campus", "wrong date", "target"} {
				cid := campus.ID
				if i == 2 {
					cid = other.ID
				}
				room := roommodel.ClinicRoom{Name: name, Enabled: i != 0, CampusID: &cid, Type: roommodel.RoomTypeDynamic}
				if err := db.Create(&room).Error; err != nil {
					t.Fatal(err)
				}
				if i == 1 {
					continue
				}
				d := date
				if i == 3 {
					d = d.AddDate(0, 0, 1)
				}
				sd := schedulingmodel.ClinicServiceDate{RoomID: &room.ID, Date: d, Capacity: 1}
				if err := db.Create(&sd).Error; err != nil {
					t.Fatal(err)
				}
				if i == 4 {
					target = room.ID
				}
			}
			for _, status := range []recordmodel.RecordStatus{recordmodel.RecordStatusRejected, recordmodel.RecordStatusReferred, recordmodel.RecordStatusNoShow, recordmodel.RecordStatusCompleted} {
				if err := db.Create(&recordmodel.ClinicRecord{RoomID: target, AppointmentTime: date, Status: status}).Error; err != nil {
					t.Fatal(err)
				}
			}
			// Other dates never contribute to the requested date's count.
			if err := db.Create(&recordmodel.ClinicRecord{RoomID: target, AppointmentTime: date.AddDate(0, 0, 2), Status: recordmodel.RecordStatusPending}).Error; err != nil {
				t.Fatal(err)
			}
			got, err := svc.PreferredRoom(context.Background(), campus, date, roomservice.ListRoomFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if future {
				if got == nil || *got != target {
					t.Fatalf("want target %d got %v", target, got)
				}
			} else if got != nil {
				t.Fatal("completed ticket must consume today's capacity")
			}
			normal := roommodel.RoomTypeNormal
			for _, f := range []roomservice.ListRoomFilter{{Name: "absent"}, {Type: &normal}} {
				got, err := svc.PreferredRoom(context.Background(), campus, date, f)
				if err != nil || got != nil {
					t.Fatalf("filter: got %v, %v", got, err)
				}
			}
			for _, date := range []time.Time{{}, today.AddDate(0, 0, -1)} {
				if _, err := svc.PreferredRoom(context.Background(), campus, date, roomservice.ListRoomFilter{}); !errors.Is(err, roomservice.ErrSelectionDate) {
					t.Fatalf("date error: %v", err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := svc.PreferredRoom(ctx, campus, date, roomservice.ListRoomFilter{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			campus.SelectionPolicy = "bad"
			if _, err := svc.PreferredRoom(context.Background(), campus, date, roomservice.ListRoomFilter{}); !errors.Is(err, roomservice.ErrCampusInvalidPolicy) {
				t.Fatalf("policy error: %v", err)
			}
		})
	}
}

func TestCampusSelectionPolicyPersistence(t *testing.T) {
	db := setupRoomServiceDB(t)
	svc := roomservice.NewCampusService(db)
	campus, err := svc.Create(roomservice.CreateCampusInput{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if campus.SelectionPolicy != roommodel.SelectionPolicyBalanced {
		t.Fatalf("default: %q", campus.SelectionPolicy)
	}
	policy := roommodel.SelectionPolicyFulfill
	if _, err = svc.Update(campus.ID, roomservice.UpdateCampusInput{SelectionPolicy: &policy}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetByID(campus.ID)
	if err != nil || got.SelectionPolicy != policy {
		t.Fatalf("persisted policy: %+v, %v", got, err)
	}
	name := "renamed"
	got, err = svc.Update(campus.ID, roomservice.UpdateCampusInput{Name: &name})
	if err != nil || got.SelectionPolicy != policy {
		t.Fatalf("omitted policy: %+v, %v", got, err)
	}
	for _, bad := range []roommodel.SelectionPolicy{"", "unknown"} {
		if _, err := svc.Update(campus.ID, roomservice.UpdateCampusInput{SelectionPolicy: &bad}); !errors.Is(err, roomservice.ErrCampusInvalidPolicy) {
			t.Fatalf("invalid update: %v", err)
		}
	}
	if _, err := svc.Create(roomservice.CreateCampusInput{Name: "bad", SelectionPolicy: "unknown"}); !errors.Is(err, roomservice.ErrCampusInvalidPolicy) {
		t.Fatalf("invalid create: %v", err)
	}
}
