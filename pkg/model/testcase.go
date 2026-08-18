package model

import "time"

type (
	CaseState = string
)

// 用例状态
const (
	CaseStateDraft      CaseState = "draft"      // 草稿
	CaseStateActive     CaseState = "active"     // 激活
	CaseStateDeprecated CaseState = "deprecated" // 废弃
	CaseStateHistory    CaseState = "history"    // 历史版本
)

// TestCase 是测试用例的基本数据结构，其中：
// ID 是数据库自增主键，没有实际业务语义
// UID 是用例的唯一标识，所有版本的同一个用例共享同一个 UID
type TestCase struct {
	ID             int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UID            int64     `json:"uid" gorm:"not null"`
	CaseID         string    `json:"case_id" gorm:"type:varchar(255);not null;uniqueIndex"`
	CaseName       string    `json:"case_name" gorm:"type:varchar(255);not null"`
	Priority       int       `json:"priority" gorm:"type:int;not null"`
	Description    string    `json:"description" gorm:"type:text"`
	PreCondition   string    `json:"pre_condition" gorm:"type:text"`
	Steps          []string  `json:"steps" gorm:"type:text[]"`
	ExpectedResult []string  `json:"expected_result" gorm:"type:text[]"`
	State          CaseState `json:"state" gorm:"type:varchar(50);not null"`
	Revision       int64     `json:"revision" gorm:"type:int;not null"`
	CreatedAt      time.Time `json:"created_at" gorm:"not null"`
}

// TestExecution 记录某个用例(uid+revision)的一次测试执行报告：
// 一份 markdown 全文，截图以 base64 data URI 内嵌在正文中，不单独存储。
type TestExecution struct {
	ID         int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	UID        int64     `json:"uid" gorm:"not null"`
	Revision   int64     `json:"revision" gorm:"not null"`
	Content    string    `json:"content" gorm:"type:text;not null"`
	ExecutedBy string    `json:"executed_by" gorm:"type:varchar(255);not null"`
	ExecutedAt time.Time `json:"executed_at" gorm:"not null"`
}
