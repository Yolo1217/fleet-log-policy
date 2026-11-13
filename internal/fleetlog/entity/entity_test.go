package entity

import "testing"

func TestStatusText(t *testing.T) {
	cases := map[string]string{
		CampaignApproved:   "已批准（未发布）",
		CampaignPiloting:   "试点中",
		CampaignPilotOK:    "试点成功",
		CampaignPaused:     "已暂停",
		CampaignPromoting:  "推广中",
		CampaignPendingEnd: "待结案",
		CampaignCompleted:  "已完成",
		CampaignStopped:    "已停止",
		CampaignRestoring:  "恢复中",
		CampaignRestored:   "已恢复",
		"unknown":          "unknown",
	}
	for input, want := range cases {
		if got := StatusText(input); got != want {
			t.Fatalf("StatusText(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestApplicationStatusText(t *testing.T) {
	cases := map[string]string{
		ApplicationWaiting:  "待审批",
		ApplicationApproved: "已批准",
		ApplicationRejected: "已退回",
		"other":             "other",
	}
	for input, want := range cases {
		if got := ApplicationStatusText(input); got != want {
			t.Fatalf("ApplicationStatusText(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestIsOpenCampaign(t *testing.T) {
	for _, s := range OpenCampaignStatuses {
		if !IsOpenCampaign(s) {
			t.Fatalf("%q 应视为未结束", s)
		}
	}
	for _, s := range []string{CampaignCompleted, CampaignStopped, CampaignRestored, ""} {
		if IsOpenCampaign(s) {
			t.Fatalf("%q 不应视为未结束", s)
		}
	}
}

func TestDomainError(t *testing.T) {
	err := Err("conflict", "状态不允许")
	if err.Code != "conflict" || err.Msg != "状态不允许" {
		t.Fatalf("错误构造异常: %+v", err)
	}
	if err.Error() != "conflict: 状态不允许" {
		t.Fatalf("Error() 格式异常: %s", err.Error())
	}
}
