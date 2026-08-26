package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"clinic-backend/models"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	roomCount    = 3
	dateCount    = 10
	recordCount  = 200
	adminAccount = "bench_admin"
	adminRole    = "admin"
)

var (
	roomNames = []string{"中关村", "沙河", "学院路"}
	roomAddrs = map[string]string{
		"中关村": "中关村校区",
		"沙河":  "沙河校区",
		"学院路": "学院路校区",
	}
)

type state struct {
	Rooms     []string          `json:"rooms"`
	Dates     []string          `json:"dates"`
	DateRooms map[string]string `json:"date_rooms"`
	Users     []string          `json:"users"`
	Records   map[string]uint   `json:"records"`
	Admin     adminState        `json:"admin"`
	Session   sessionState      `json:"session"`
}

type adminState struct {
	AccountID string `json:"account_id"`
	Role      string `json:"role"`
}

type sessionState struct {
	SessionToken string `json:"session_token"`
	CSRFToken    string `json:"csrf_token"`
}

func main() {
	rand.Seed(42)

	wd, _ := os.Getwd()
	dbPath := filepath.Join(wd, "clinic.db")
	if err := os.Remove(dbPath); err != nil && !os.IsNotExist(err) {
		log.Fatalf("failed to remove old clinic.db: %v", err)
	}

	db, err := gorm.Open(sqlite.Open("clinic.db?_busy_timeout=30000&_journal_mode=WAL"), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}

	if err := db.AutoMigrate(
		&models.ClinicAnnouncement{},
		&models.ClinicServiceDate{},
		&models.ClinicRoom{},
		&models.ClinicStaff{},
		&models.ClinicRecord{},
		&models.AuthSession{},
	); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)

	// Rooms
	rooms := make([]models.ClinicRoom, 0, roomCount)
	roomByName := make(map[string]uint)
	for _, name := range roomNames {
		r := models.ClinicRoom{Name: name, Address: roomAddrs[name], Enabled: true}
		if err := db.Create(&r).Error; err != nil {
			log.Fatalf("create room %s: %v", name, err)
		}
		rooms = append(rooms, r)
		roomByName[name] = r.ID
	}

	// Service dates: 10 days distributed across 3 rooms.
	distribution := []int{0, 0, 0, 0, 1, 1, 1, 2, 2, 2}
	dates := make([]models.ClinicServiceDate, 0, dateCount)
	dateStrings := make([]string, 0, dateCount)
	dateRooms := make(map[string]string, dateCount)
	for i, roomIdx := range distribution {
		d := today.AddDate(0, 0, i+1)
		roomID := rooms[roomIdx].ID
		roomName := roomNames[roomIdx]
		sd := models.ClinicServiceDate{
			Capacity:  10000,
			RoomID:    &roomID,
			Date:      d,
			StartTime: d.Add(18*time.Hour + 30*time.Minute),
			EndTime:   d.Add(21 * time.Hour),
			Title:     "正常服务",
		}
		if err := db.Create(&sd).Error; err != nil {
			log.Fatalf("create service date: %v", err)
		}
		dates = append(dates, sd)
		dateStrings = append(dateStrings, d.Format("2006-01-02"))
		dateRooms[d.Format("2006-01-02")] = roomName
	}

	// Announcement
	ann := models.ClinicAnnouncement{
		Title:          "欢迎使用诊所管理系统",
		Content:        "这是诊所管理系统的性能测试实例。",
		Tag:            models.AnnouncementTagPinned,
		CreatedTime:    now,
		LastEditedTime: now,
		ExpireDate:     today.AddDate(0, 0, 30),
		Priority:       1,
		Brief:          "欢迎使用！",
	}
	if err := db.Create(&ann).Error; err != nil {
		log.Fatalf("create announcement: %v", err)
	}

	// Admin staff
	staff := models.ClinicStaff{
		AccountID: adminAccount,
		Realname:  "Benchmark Admin",
		PhoneNum:  "13800138000",
		Role:      adminRole,
	}
	if err := db.Create(&staff).Error; err != nil {
		log.Fatalf("create admin staff: %v", err)
	}

	// Records
	statuses := []models.RecordStatus{
		models.RecordStatusPending,
		models.RecordStatusConfirmed,
		models.RecordStatusConfirmed,
		models.RecordStatusArrived,
		models.RecordStatusArrived,
		models.RecordStatusInProgress,
		models.RecordStatusCompleted,
	}
	problems := []string{
		"电脑无法开机，按下电源键无反应",
		"屏幕间歇性闪烁，外接显示器正常",
		"键盘部分按键失灵，尤其是空格键",
		"系统反复蓝屏，错误代码 0x0000001a",
		"电池续航严重下降，需更换电池",
		"电源适配器损坏",
		"疑似主板短路，需专业检测",
		"无线网络经常断开，已尝试更新驱动",
		"风扇噪音过大，机身温度异常",
		"硬盘读写速度明显下降",
	}

	records := make([]models.ClinicRecord, 0, recordCount)
	userList := make([]string, 0, recordCount)
	for n := 1; n <= recordCount; n++ {
		username := fmt.Sprintf("user%03d", n)
		userList = append(userList, username)
		sd := dates[rand.Intn(len(dates))]
		records = append(records, models.ClinicRecord{
			User:            username,
			Realname:        fmt.Sprintf("学生%d", n),
			PhoneNum:        fmt.Sprintf("188%08d", n),
			Status:          statuses[rand.Intn(len(statuses))],
			AppointmentTime: sd.Date,
			QuestionDesc:    problems[rand.Intn(len(problems))],
			RoomID:          *sd.RoomID,
		})
	}

	if err := db.CreateInBatches(records, 100).Error; err != nil {
		log.Fatalf("create records: %v", err)
	}

	// Map username -> record id.
	recordMap := make(map[string]uint, recordCount)
	var saved []models.ClinicRecord
	if err := db.Where("user LIKE ?", "user%").Order("id").Find(&saved).Error; err != nil {
		log.Fatalf("fetch saved records: %v", err)
	}
	for _, rec := range saved {
		recordMap[rec.User] = rec.ID
	}

	// Session for admin staff.
	sessionToken, csrfToken, err := createAdminSession(db, staff.ID)
	if err != nil {
		log.Fatalf("create admin session: %v", err)
	}

	st := state{
		Rooms:     roomNames,
		Dates:     dateStrings,
		DateRooms: dateRooms,
		Users:     userList,
		Records:   recordMap,
		Admin:     adminState{AccountID: adminAccount, Role: adminRole},
		Session:   sessionState{SessionToken: sessionToken, CSRFToken: csrfToken},
	}

	statePath := filepath.Join(wd, "..", "clinic-benchmark", "data", "new_state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		log.Fatalf("create state dir: %v", err)
	}
	f, err := os.Create(statePath)
	if err != nil {
		log.Fatalf("create state file: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(st); err != nil {
		log.Fatalf("encode state: %v", err)
	}

	fmt.Printf("Seeded new backend:\n")
	fmt.Printf("  Rooms: %d\n", len(rooms))
	fmt.Printf("  Dates: %d\n", len(dates))
	fmt.Printf("  Records: %d\n", len(recordMap))
	fmt.Printf("  Admin staff: %s (%s)\n", adminAccount, adminRole)
	fmt.Printf("  State written to: %s\n", statePath)
}

func createAdminSession(db *gorm.DB, staffID int) (string, string, error) {
	sessionToken, err := generateToken()
	if err != nil {
		return "", "", err
	}
	csrfToken, err := generateToken()
	if err != nil {
		return "", "", err
	}
	sess := models.AuthSession{
		TokenHash:     hashToken(sessionToken),
		StaffID:       staffID,
		Role:          adminRole,
		CSRFTokenHash: hashToken(csrfToken),
		CASTicket:     "bench-seed",
		ExpiresAt:     time.Now().UTC().Add(7 * 24 * time.Hour),
	}
	if err := db.Create(&sess).Error; err != nil {
		return "", "", err
	}
	return sessionToken, csrfToken, nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
