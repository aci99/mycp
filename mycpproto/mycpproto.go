package mycpproto

import "time"

type MyCPPackageStatus int64

const (
	MyCPPackageStatusFail MyCPPackageStatus = iota
	MyCPPackageStatusSucc
	MyCPPackageStatusNoNeedToCP
)

type MyCPPackage struct {
	SrcPath         string
	DstPath         string
	Data            []byte
	Status          MyCPPackageStatus
	SrcIsDir        bool
	MyFileInfoSlice []MyFileInfo
	LastMyCPTime    time.Time
	OnlyModified    bool
	Password        string
	Direction       DirectionT
}

type MyFileInfo struct {
	Name  string
	IsDir bool
}

type MyCPInfo struct {
	Path2LastMyCPTime map[string]time.Time
	LastRemoteHost    string
}

type DirectionT int

const (
	DirectionRemoteIsSrc DirectionT = iota
	DirectionRemoteIsDst
)
