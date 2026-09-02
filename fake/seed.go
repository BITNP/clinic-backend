package main

import (
	"fmt"
	"log"
	"time"

	"clinic-backend/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	db, err := gorm.Open(sqlite.Open("clinic.db"), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	if err := db.AutoMigrate(
		&models.ClinicAnnouncement{},
		&models.ClinicServiceDate{},
		&models.ClinicRoom{},
		&models.ClinicStaff{},
		&models.ClinicStaffWorkyear{},
		&models.ClinicWorkSchedule{},
		&models.ClinicWorkScheduleWeekday{},
		&models.ClinicWorkScheduleStaff{},
		&models.ClinicRecord{},
		&models.ClinicRecordDevice{},
		&models.ClinicRecordWorker{},
		&models.ClinicRecordArrival{},
		&models.ClinicRecordRejection{},
		&models.ClinicRecordReferral{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)

	// ── Rooms ──────────────────────────────────────────────────────────────
	roomDefs := []struct {
		Name    string
		Address string
	}{
		{"中关村", "中关村校区"},
		{"沙河", "沙河校区"},
		{"学院路", "学院路校区"},
		{"望京", "望京校区"},
		{"朝阳", "朝阳校区"},
	}
	var roomIDs []uint
	for _, rd := range roomDefs {
		r := models.ClinicRoom{Name: rd.Name, Address: rd.Address, Enabled: true}
		if err := db.Where("name = ?", r.Name).FirstOrCreate(&r).Error; err != nil {
			log.Fatalf("seed room %s: %v", r.Name, err)
		}
		roomIDs = append(roomIDs, r.ID)
		fmt.Printf("room: %s (id=%d)\n", r.Name, r.ID)
	}

	// Build name→ID lookup.
	roomByName := make(map[string]uint)
	for _, rd := range roomDefs {
		var r models.ClinicRoom
		db.Where("name = ?", rd.Name).First(&r)
		roomByName[rd.Name] = r.ID
	}
	roomZG := roomByName["中关村"]
	roomSH := roomByName["沙河"]
	roomXYL := roomByName["学院路"]
	roomWJ := roomByName["望京"]
	roomCY := roomByName["朝阳"]

	// ── Service dates (tomorrow and day after) ─────────────────────────────
	tomorrow := today.AddDate(0, 0, 1)
	dayAfter := today.AddDate(0, 0, 2)

	sdZG := models.ClinicServiceDate{
		Capacity:  15,
		RoomID:    &roomZG,
		Date:      tomorrow,
		StartTime: tomorrow.Add(10*time.Hour + 30*time.Minute),
		EndTime:   tomorrow.Add(13 * time.Hour),
		Title:     "正常服务",
	}
	if err := db.Where("room_id = ? AND date = ?", sdZG.RoomID, sdZG.Date).FirstOrCreate(&sdZG).Error; err != nil {
		log.Fatalf("seed service date zg: %v", err)
	}
	fmt.Printf("service-date: %s room=%d (id=%d)\n", sdZG.Date.Format("2006-01-02"), *sdZG.RoomID, sdZG.ID)

	sdSH := models.ClinicServiceDate{
		Capacity:  10,
		RoomID:    &roomSH,
		Date:      dayAfter,
		StartTime: dayAfter.Add(10*time.Hour + 30*time.Minute),
		EndTime:   dayAfter.Add(13 * time.Hour),
		Title:     "正常服务",
	}
	if err := db.Where("room_id = ? AND date = ?", sdSH.RoomID, sdSH.Date).FirstOrCreate(&sdSH).Error; err != nil {
		log.Fatalf("seed service date sh: %v", err)
	}
	fmt.Printf("service-date: %s room=%d (id=%d)\n", sdSH.Date.Format("2006-01-02"), *sdSH.RoomID, sdSH.ID)

	// ── Staff records ────────────────────────────────────────────────────
	staffDefs := []struct {
		AccountID string
		Realname  string
		Email     string
		Role      string
		WorkYears []int
	}{
		{AccountID: "manualuser", Realname: "Manual Tester", Email: "manual@example.com", Role: "", WorkYears: []int{2025, 2026}},
		{AccountID: "zhangsan", Realname: "张三", Email: "zhangsan@example.com", Role: "", WorkYears: []int{2024, 2025, 2026}},
		{AccountID: "lisi", Realname: "李四", Email: "lisi@example.com", Role: "", WorkYears: []int{2025, 2026}},
		{AccountID: "wangwu", Realname: "王五", Email: "wangwu@example.com", Role: ""},
		{AccountID: "zhaoliu", Realname: "赵六", Email: "zhaoliu@example.com", Role: "", WorkYears: []int{2026}},
		{AccountID: "sunqi", Realname: "孙七", Email: "sunqi@example.com", Role: ""},
		{AccountID: "admin", Realname: "管理员", Email: "admin@example.com", Role: "admin", WorkYears: []int{2023, 2024, 2025, 2026}},
	}
	staffByAccount := make(map[string]int)
	for _, sd := range staffDefs {
		s := models.ClinicStaff{
			AccountID: sd.AccountID,
			Realname:  sd.Realname,
			Email:     sd.Email,
			Role:      sd.Role,
		}
		if err := db.Where("account_id = ?", s.AccountID).FirstOrCreate(&s).Error; err != nil {
			log.Fatalf("seed staff %s: %v", s.AccountID, err)
		}
		fmt.Printf("staff: %s (id=%d)\n", s.AccountID, s.ID)
		staffByAccount[s.AccountID] = s.ID

		for _, year := range sd.WorkYears {
			wy := models.ClinicStaffWorkyear{StaffID: s.ID, WorkYear: year}
			if err := db.Where("staff_id = ? AND work_year = ?", wy.StaffID, wy.WorkYear).FirstOrCreate(&wy).Error; err != nil {
				log.Fatalf("seed workyear %d %d: %v", s.ID, year, err)
			}
		}
	}

	// ── Work schedule: 默认排班 ───────────────────────────────────────────
	type weekdayDef struct {
		Weekday   int
		StartHour int
		StartMin  int
		EndHour   int
		EndMin    int
		Room      string
	}

	weekFromNow := today.AddDate(0, 0, 7)
	twoWeeksFromNow := today.AddDate(0, 0, 14)

	createSchedule := func(name string, startDate, endDate time.Time, weekdayDefs []weekdayDef, assignments map[int][]string) *models.ClinicWorkSchedule {
		sch := models.ClinicWorkSchedule{
			Name:      name,
			StartDate: startDate,
			EndDate:   endDate,
		}
		if err := db.Where("name = ? AND start_date = ?", sch.Name, sch.StartDate).FirstOrCreate(&sch).Error; err != nil {
			log.Fatalf("seed work schedule %s: %v", name, err)
		}
		fmt.Printf("work-schedule: %s (id=%d, %s ~ %s)\n", sch.Name, sch.ID, sch.StartDate.Format("2006-01-02"), sch.EndDate.Format("2006-01-02"))

		var savedWeekdays []models.ClinicWorkScheduleWeekday
		for _, wd := range weekdayDefs {
			day := startDate.AddDate(0, 0, wd.Weekday-1)
			entry := models.ClinicWorkScheduleWeekday{
				WorkScheduleID: sch.ID,
				Weekday:        wd.Weekday,
				StartTime:      day.Add(time.Duration(wd.StartHour)*time.Hour + time.Duration(wd.StartMin)*time.Minute),
				EndTime:        day.Add(time.Duration(wd.EndHour)*time.Hour + time.Duration(wd.EndMin)*time.Minute),
				RoomID:         roomByName[wd.Room],
			}
			if err := db.Where("work_schedule_id = ? AND weekday = ? AND room_id = ? AND start_time = ?",
				entry.WorkScheduleID, entry.Weekday, entry.RoomID, entry.StartTime).
				FirstOrCreate(&entry).Error; err != nil {
				log.Fatalf("seed schedule weekday: %v", err)
			}
			savedWeekdays = append(savedWeekdays, entry)
			fmt.Printf("schedule-weekday: weekday=%d room=%s (id=%d)\n", entry.Weekday, wd.Room, entry.ID)
		}

		for _, weekdayEntry := range savedWeekdays {
			staffAccounts, ok := assignments[weekdayEntry.Weekday]
			if !ok {
				continue
			}
			for _, account := range staffAccounts {
				staffID, ok := staffByAccount[account]
				if !ok {
					log.Fatalf("staff account %s not found", account)
				}
				ss := models.ClinicWorkScheduleStaff{
					WeekdayID:  weekdayEntry.ID,
					StaffID:    staffID,
					ScheduleID: sch.ID,
				}
				if err := db.Where("weekday_id = ? AND staff_id = ? AND schedule_id = ?", ss.WeekdayID, ss.StaffID, ss.ScheduleID).FirstOrCreate(&ss).Error; err != nil {
					log.Fatalf("seed schedule staff: %v", err)
				}
				fmt.Printf("schedule-staff: weekday=%d staff=%s (id=%d)\n", weekdayEntry.Weekday, account, ss.ID)
			}
		}
		return &sch
	}

	mustGetID := func(m map[string]int, key string) int {
		id, ok := m[key]
		if !ok {
			log.Fatalf("missing key %q", key)
		}
		return id
	}

	createSchedule("默认排班", weekFromNow, twoWeeksFromNow,
		[]weekdayDef{
			{Weekday: 1, StartHour: 9, StartMin: 0, EndHour: 12, EndMin: 0, Room: "中关村"},
			{Weekday: 1, StartHour: 14, StartMin: 0, EndHour: 17, EndMin: 0, Room: "沙河"},
			{Weekday: 3, StartHour: 9, StartMin: 0, EndHour: 12, EndMin: 0, Room: "中关村"},
			{Weekday: 5, StartHour: 14, StartMin: 0, EndHour: 17, EndMin: 0, Room: "沙河"},
		},
		map[int][]string{
			1: {"zhangsan", "lisi"},
			3: {"wangwu"},
			5: {"zhangsan", "wangwu"},
		},
	)

	// ── Work schedule: 2026春季 ──────────────────────────────────────────
	springStart := today.AddDate(0, 0, 30)
	springEnd := today.AddDate(0, 0, 60)

	createSchedule("2026春季", springStart, springEnd,
		[]weekdayDef{
			{Weekday: 1, StartHour: 9, StartMin: 0, EndHour: 12, EndMin: 0, Room: "中关村"},
			{Weekday: 2, StartHour: 10, StartMin: 0, EndHour: 14, EndMin: 0, Room: "学院路"},
			{Weekday: 4, StartHour: 13, StartMin: 0, EndHour: 17, EndMin: 0, Room: "望京"},
			{Weekday: 5, StartHour: 9, StartMin: 0, EndHour: 12, EndMin: 0, Room: "朝阳"},
		},
		map[int][]string{
			1: {"zhaoliu"},
			2: {"zhangsan", "sunqi"},
			4: {"lisi", "zhaoliu"},
			5: {"wangwu", "sunqi"},
		},
	)

	// ── Announcement ──────────────────────────────────────────────────────
	ann := models.ClinicAnnouncement{
		Title:          "欢迎使用诊所管理系统",
		Content:        "这是诊所管理系统的测试实例。\n\n请查看公告、服务时间和管理工单。",
		Tag:            models.AnnouncementTagPinned,
		CreatedTime:    now,
		LastEditedTime: now,
		ExpireDate:     today.AddDate(0, 0, 30),
		Priority:       1,
		Brief:          "欢迎使用！",
	}
	if err := db.Where("title = ?", ann.Title).FirstOrCreate(&ann).Error; err != nil {
		log.Fatalf("seed announcement: %v", err)
	}
	fmt.Printf("announcement: %s (id=%d)\n", ann.Title, ann.ID)

	// ── Tickets (ClinicRecord) ──────────────────────────────────────────────
	type ticketDef struct {
		User            string
		Realname        string
		PhoneNum        string
		Status          models.RecordStatus
		AppointmentDate time.Time
		QuestionDesc    string
		RoomID          uint
	}

	ticketDefs := []ticketDef{
		{User: "student01", Realname: "学生A", PhoneNum: "18800000001", Status: models.RecordStatusPending, AppointmentDate: tomorrow, QuestionDesc: "电脑无法开机，按下电源键无反应", RoomID: roomZG},
		{User: "student02", Realname: "学生B", PhoneNum: "18800000002", Status: models.RecordStatusConfirmed, AppointmentDate: today.AddDate(0, 0, 3), QuestionDesc: "屏幕间歇性闪烁，外接显示器正常", RoomID: roomSH},
		{User: "student03", Realname: "学生C", PhoneNum: "18800000003", Status: models.RecordStatusArrived, AppointmentDate: today.AddDate(0, 0, 2), QuestionDesc: "键盘部分按键失灵，尤其是空格键", RoomID: roomZG},
		{User: "student04", Realname: "学生D", PhoneNum: "18800000004", Status: models.RecordStatusInProgress, AppointmentDate: tomorrow, QuestionDesc: "系统反复蓝屏，错误代码 0x0000001a", RoomID: roomXYL},
		{User: "student05", Realname: "学生E", PhoneNum: "18800000005", Status: models.RecordStatusCompleted, AppointmentDate: today.AddDate(0, 0, -1), QuestionDesc: "电池续航严重下降，需更换电池", RoomID: roomWJ},
		{User: "student06", Realname: "学生F", PhoneNum: "18800000006", Status: models.RecordStatusRejected, AppointmentDate: today.AddDate(0, 0, 4), QuestionDesc: "电源适配器损坏", RoomID: roomCY},
		{User: "student07", Realname: "学生G", PhoneNum: "18800000007", Status: models.RecordStatusReferred, AppointmentDate: today.AddDate(0, 0, 5), QuestionDesc: "疑似主板短路，需专业检测", RoomID: roomZG},
	}

	var savedRecordIDs []uint
	for _, td := range ticketDefs {
		rec := models.ClinicRecord{
			User:            td.User,
			Realname:        td.Realname,
			PhoneNum:        td.PhoneNum,
			Status:          td.Status,
			AppointmentTime: td.AppointmentDate,
			QuestionDesc:    td.QuestionDesc,
			RoomID:          td.RoomID,
		}
		if err := db.Where(`"user" = ? AND appointment_time = ? AND room = ?`, rec.User, rec.AppointmentTime, rec.RoomID).FirstOrCreate(&rec).Error; err != nil {
			log.Fatalf("seed ticket %s: %v", td.User, err)
		}
		fmt.Printf("ticket: %s status=%s (id=%d)\n", td.User, td.Status, rec.ID)
		savedRecordIDs = append(savedRecordIDs, rec.ID)
	}

	// Attach optional side tables for specific tickets.
	// Ticket 3 (student03, arrived) → arrival time
	if err := db.Where("record_id = ?", savedRecordIDs[2]).FirstOrCreate(&models.ClinicRecordArrival{
		RecordID: savedRecordIDs[2], ArriveTime: now.Add(-30 * time.Minute),
	}).Error; err != nil {
		log.Fatalf("seed arrival: %v", err)
	}
	fmt.Printf("ticket-arrival: record_id=%d\n", savedRecordIDs[2])

	// Ticket 4 (student04, in_progress) → worker assignment, no finish time
	if err := db.Where("record_id = ?", savedRecordIDs[3]).FirstOrCreate(&models.ClinicRecordWorker{
		RecordID: savedRecordIDs[3], WorkerID: uint(mustGetID(staffByAccount, "zhangsan")), WorkerDesc: "诊断中，疑似内存故障",
	}).Error; err != nil {
		log.Fatalf("seed worker: %v", err)
	}
	fmt.Printf("ticket-worker: record_id=%d\n", savedRecordIDs[3])

	// Ticket 5 (student05, completed) → worker assignment with finish time and device info
	if err := db.Where("record_id = ?", savedRecordIDs[4]).FirstOrCreate(&models.ClinicRecordWorker{
		RecordID: savedRecordIDs[4], WorkerID: uint(mustGetID(staffByAccount, "lisi")),
		WorkerDesc: "已更换电池，测试正常", FinishTime: now,
	}).Error; err != nil {
		log.Fatalf("seed worker completed: %v", err)
	}
	fmt.Printf("ticket-worker: record_id=%d\n", savedRecordIDs[4])

	if err := db.Where("record_id = ?", savedRecordIDs[4]).FirstOrCreate(&models.ClinicRecordDevice{
		RecordID: savedRecordIDs[4], LaptopModel: "ThinkPad X1 Carbon Gen 9",
	}).Error; err != nil {
		log.Fatalf("seed device: %v", err)
	}
	fmt.Printf("ticket-device: record_id=%d\n", savedRecordIDs[4])

	// Ticket 6 (student06, rejected) → rejection reason
	if err := db.Where("record_id = ?", savedRecordIDs[5]).FirstOrCreate(&models.ClinicRecordRejection{
		RecordID: savedRecordIDs[5], RejectReason: "非维修范围：电源适配器需自行购买",
	}).Error; err != nil {
		log.Fatalf("seed rejection: %v", err)
	}
	fmt.Printf("ticket-rejection: record_id=%d\n", savedRecordIDs[5])

	// Ticket 7 (student07, referred) → referral reason
	if err := db.Where("record_id = ?", savedRecordIDs[6]).FirstOrCreate(&models.ClinicRecordReferral{
		RecordID: savedRecordIDs[6], ReferralReason: "主板级维修需转至官方售后服务中心",
	}).Error; err != nil {
		log.Fatalf("seed referral: %v", err)
	}
	fmt.Printf("ticket-referral: record_id=%d\n", savedRecordIDs[6])

	fmt.Println("\nSeed complete. Run 'go run main.go' to start the backend.")
}
