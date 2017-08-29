package mjhp

import (
	"log"
	"fmt"
	"sort"
)

// 判断请求
// Mask 第0位 	是否略过翻数判断 （翻数判断，必定缺一门）
// Mask 第1位	宜宾麻将
type JudgeReq struct {
	Id      int `json:"id"`
	Hands   []int `json:"h"`
	Events  []MjEvent `json:"e"`
	LzCount int `json:"c"`
	LzTotal int `json:"t"` // 总癞子数

	//FromTopic string `json:"-"`
	//TableId   string `json:"-"`
	BenJin  int `json:"-"` // 本金
	JudgeMj int `json:"-"` // 被判定的麻将
	MaxRate int `json:"-"` // 最大番数
	Mask    int `json:"-"`

	colorCount    int8          // 预解析，颜色数
	hands         []byte        // 手牌
	handsWithLz   []byte        // 手牌带癞子
	rateAlgorithm RateAlgorithm // 番数计算
}

// 批量，一个桌子每个人的判定
type JudgeReqBatch struct {
	FromTopic string `json:"from"`
	TableId   string `json:"ti"`
	List      []JudgeReq `json:"l"`
	JudgeMj   int `json:"m"`
	BenJin    int `json:"bj"` // 本金
	MaxRate   int `json:"r"`  // 最大番数
	Mask    int `json:"s"`
}

func (this *JudgeReq) IsSkipRate() bool {
	return this.Mask&0x01 == 0x01
}

// 是否为宜宾麻将
func (this *JudgeReq) IsYiBinMj() bool {
	return this.Mask&0x02 == 0x02
}

// 本金
func (this *JudgeReq) GetBenJinByte() byte {
	return tiles[this.BenJin]
}

func (this *JudgeReq) PreAnalysis() {
	lenArr := len(this.Hands)
	if this.LzCount < 0 {
		return
	}
	this.hands = make([]byte, lenArr)
	for idx, v := range this.Hands {
		p := v % 9
		t := v / 9
		switch t {
		case 0:
			this.hands[idx] = 0x01 + byte(p)
		case 1:
			this.hands[idx] = 0x11 + byte(p)
		case 2:
			this.hands[idx] = 0x21 + byte(p)
		}
		// 东风西风暂时不做
	}
	sort.Sort(byteSlice(this.hands))
	this.handsWithLz = this.hands
	this.colorCount = colorCount(this.hands, this.Events)
	if this.LzCount > 0 {
		this.handsWithLz = make([]byte, 14, 14)
	}
	if this.IsYiBinMj() {
		this.rateAlgorithm = yiBinRate
	}

}

const MjEvent_Type_Peng = 1
const MjEvent_Type_Gang = 2

// 麻将事件区事件
// 1碰，2杠
type MjEvent struct {
	Type int8 `json:"t"`
	Key  int `json:"k"`
}

// 0,1,2 万筒条
func (this *MjEvent) GetColorType() int8 {
	return int8(this.Key / 8)
}

func (event MjEvent) IsGang() bool {
	return event.Type == MjEvent_Type_Gang
}

func (event MjEvent) IsPeng() bool {
	return event.Type == MjEvent_Type_Peng
}

// 麻将int表示 转 byte表示
type Mj int

func (this Mj) ToByte() byte {
	return tiles[int(this)]
}

// 判断结果
type JudgeResult struct {
	Id     int `json:"id"`
	Result bool `json:"r"`
	Rate   int `json:"t"`
}

// 批量的判定结果
type JudgeBatchResult struct {
	TableId   string `json:"ti"`
	List      []*JudgeResult `json:"l"`
	fromTopic string
}

func (this *JudgeResult) Success(req *JudgeReq, rst *RateResult) {
	this.Result = true
	rate := req.rateAlgorithm.Calculate(req, rst)
	if (this.Rate & 0xf) < rate {
		this.Rate = (rst.Mask << 8) + rate
	}
}

func (this *JudgeResult) IsRateFull(req *JudgeReq) bool {
	return req.MaxRate <= (this.Rate * 0xf)
}

type mj int

func (this mj) ToString() string {
	t := this / 9
	p := this % 9
	switch t {
	case 0:
		return fmt.Sprintf("%d万", p+1)
	case 1:
		return fmt.Sprintf("%d筒", p+1)
	case 2:
		return fmt.Sprintf("%d条", p+1)
	default:
		return "未知"
	}
}

var workChan chan *JudgeReqBatch
var workRun = true

// 开启计算进程组
func StartComputeWork() {
	workChan = make(chan *JudgeReqBatch, 1024)
	for i := 0; i < cfg.ComputeNum; i++ {
		go startComputeWorkImpl()
	}
}

func ShutdownComputeWork() {
	workRun = false
}

// 开启工作进程
func startComputeWorkImpl() {
	for workRun {
		select {
		case req := <-workChan:
			judgeHuBatch(req)
		}
	}
}
func judgeHuBatch(batch *JudgeReqBatch) {
	if batch.List == nil {
		log.Println("judgeHuBatch error, list is empty: ", batch)
	}
	batchRst := &JudgeBatchResult{
		TableId:   batch.TableId,
		fromTopic: batch.FromTopic,
		List:      make([]*JudgeResult, 0, len(batch.List)),
	}
	for _, r := range batch.List {
		r.BenJin = batch.BenJin
		if batch.JudgeMj >= 0 {
			r.Hands = append(r.Hands, batch.JudgeMj)
		}
		r.JudgeMj = batch.JudgeMj
		r.MaxRate = batch.MaxRate
		r.Mask = batch.Mask
		rst := judgeHu(&r)
		if rst.Result {
			log.Printf("可胡, rate: %d倍 %s", rst.Rate&0xf, rateToString(rst.Rate>>8))
		} else {
			log.Println("不能胡")
		}
		batchRst.List = append(batchRst.List, rst)
	}
	log.Println("batchRst: ", batchRst)
	sendChan <- batchRst
}

func TestJudgeHu(arr []int, lzCount int, benJin int) {
	req := &JudgeReq{Hands: arr, LzCount: lzCount, rateAlgorithm: yiBinRate, LzTotal: lzCount,
		BenJin: benJin}
	resp := judgeHu(req)
	log.Printf("%b\n", resp.Rate)
	if resp.Result {
		log.Printf("可胡, rate: %d倍 %s", resp.Rate&0xf, rateToString(resp.Rate>>8))
	} else {
		log.Println("不能胡")
	}
}
func lzArrToString(arr []int) []string {
	ss := make([]string, len(arr))
	for idx, v := range arr {
		ss[idx] = mj(v).ToString()
	}
	return ss
}

func judgeHu(req *JudgeReq) (rst *JudgeResult) {
	//printMask(hands)
	req.PreAnalysis()
	printMj(req.hands)
	rst = &JudgeResult{}
	switch req.LzCount {
	case 0:
		judge0(req, rst)
	case 1:
		judge1(req, rst)
	case 2:
		judge2(req, rst)
	case 3:
		judge3(req, rst)
	case 4:
		judge4(req, rst)
	case 5:
		judge5(req, rst)
	case 6:
		judge6(req, rst)
	case 7:
		judge7(req, rst)
	case 8:
		judge8(req, rst)
	}
	return
}

func judge0(req *JudgeReq, rst *JudgeResult) {
	rst.Result = isCanHu(req.hands)
	if rst.Result && !req.IsSkipRate() {
		// 判断番数
		rate := JudgeRate(req)
		rst.Success(req, rate)
	}
}

func judge1(req *JudgeReq, rst *JudgeResult) {
	skipRate := req.IsSkipRate()
	for _, mj := range tiles {
		copy(req.handsWithLz, req.hands)
		req.handsWithLz[13] = mj
		//printMj(bak)
		if isCanHu(req.handsWithLz) {
			if skipRate {
				rst.Result = true
				return
			}
			rate := JudgeRate(req)
			rst.Success(req, rate)
			if rst.IsRateFull(req) {
				return
			}
		}
	}
}

func judge2(req *JudgeReq, rst *JudgeResult) {
	bak := req.handsWithLz
	skipRate := req.IsSkipRate()
	for _, m0 := range tiles {
		for _, m1 := range tiles {
			copy(bak, req.hands)
			bak[12] = m0
			bak[13] = m1
			if isCanHu(bak) {
				if skipRate {
					rst.Result = true
					return
				}
				rate := JudgeRate(req)
				rst.Success(req, rate)
				if rst.IsRateFull(req) {
					return
				}
			}
		}
	}
}

func judge3(req *JudgeReq, rst *JudgeResult) {
	bak := req.handsWithLz
	skipRate := req.IsSkipRate()
	for _, m0 := range tiles {
		for _, m1 := range tiles {
			for _, m2 := range tiles {
				copy(bak, req.hands)
				bak[11] = m0
				bak[12] = m1
				bak[13] = m2
				if isCanHu(bak) {
					if skipRate {
						rst.Result = true
						return
					}
					rate := JudgeRate(req)
					rst.Success(req, rate)
					if rst.IsRateFull(req) {
						return
					}
				}
			}
		}
	}
}

func judge4(req *JudgeReq, rst *JudgeResult) {
	hands := req.hands
	bak := req.handsWithLz
	req.hands = bak
	skipRate := req.IsSkipRate()
	//i := 0
	for _, m0 := range tiles {
		for _, m1 := range tiles {
			for _, m2 := range tiles {
				for _, m3 := range tiles {
					copy(bak, hands)
					bak[10] = m0
					bak[11] = m1
					bak[12] = m2
					bak[13] = m3
					if isCanHu(bak) {
						if skipRate {
							rst.Result = true
							return
						}
						rate := JudgeRate(req)
						rst.Success(req, rate)
						printMj(bak)
						if rst.IsRateFull(req) {
							return
						}
					}
				}
			}
		}
	}
}

// 5张听用牌
func judge5(req *JudgeReq, result *JudgeResult) {
	lenOfHands := len(req.hands)
	base := judgeBaseRate(req)
	if lenOfHands == 3 || lenOfHands == 0 {
		base := judgeBaseRate(req)
		base.Mask |= RATE_MASK_DUIDUI_HU
		if req.colorCount == 1 {
			base.Mask |= RATE_MASK_QING_YI_SE
		}
	} else {
		// 双色
		c2, c3 := judgeC23(req.hands)
		if lenOfHands-int(c2)*2-int(c3)*3 <= 5 {
			base.Mask |= RATE_MASK_DUIDUI_HU
		}
		if req.colorCount == 1 {
			base.Mask |= RATE_MASK_QING_YI_SE
		} else {
			if lenOfHands == 6 { // 6张手牌
				if !isCanHuLz5_6(req.hands) {
					return
				}
			} else { // 9张手牌
				if !isCanHuLz5_9(req.hands) {
					return
				}
			}
		}
	}
	result.Success(req, base)
}

// 6张听用牌
func judge6(req *JudgeReq, result *JudgeResult) {
	base := judgeBaseRate(req)
	if len(req.hands) == 8 {
		if req.colorCount == 1 {
			base.Mask |= RATE_MASK_QING_YI_SE
		} else {
			c2, c3 := judgeC23(req.hands)
			if c2+c3 > 0 {
				base.Mask |= RATE_MASK_7DUI
			} else {
				// 走通用判定
				judge6Common(base, req, result)
				return
			}
		}
	} else {
		base.Mask |= RATE_MASK_DUIDUI_HU
	}
	result.Success(req, base)
}

// 6张牌通用判定
func judge6Common(base *RateResult, req *JudgeReq, result *JudgeResult) {
	if isCanHuLz6_8(req.hands) {
		if req.IsSkipRate() {
			result.Result = true
		} else {
			result.Success(req, base)
		}
	}
}

// 7张听用牌
func judge7(req *JudgeReq, result *JudgeResult) {
	if req.IsSkipRate() {
		result.Result = true
		return
	}
	base := judgeBaseRate(req)
	log.Printf("judge7 %b\n", base.Mask)
	if len(req.hands) == 7 {
		base.Mask |= RATE_MASK_7DUI
	} else {
		base.Mask |= RATE_MASK_DUIDUI_HU
	}
	log.Printf("judge711 %b\n", base.Mask)
	result.Success(req, base)
}

func judge8(req *JudgeReq, result *JudgeResult) {
	log.Println("judge8")
	// 是否只检查可胡， 8个癞子任意胡
	if req.IsSkipRate() {
		result.Result = true
		return
	}
	// 最大胡法
	//lenOfHands := len(req.hands)
	base := judgeBaseRate(req)
	if len(req.hands) == 6 {
		base.Mask |= RATE_MASK_7DUI
	} else {
		base.Mask |= RATE_MASK_DUIDUI_HU
	}
	result.Success(req, base)
}

func judgeC23(hands []byte) (c2 int8, c3 int8) {
	lenOfHands := len(hands)
	for i := 0; i < lenOfHands-1; {
		if hands[i] == hands[i+1] { // 2个相等
			if i+2 < lenOfHands && hands[i] == hands[i+2] { // 三个相等
				if i+3 < lenOfHands && hands[i] == hands[i+3] { // 4个相等
					i += 4
				} else {
					c3++
					i += 3
				}
			} else {
				c2++
				i += 2
			}
		} else {
			i++
		}
	}
	return
}

func colorCount(hands []byte, events []MjEvent) int8 {
	var mask byte = 0x00
	for _, b := range hands {
		if b < 0x10 {
			mask |= 0x01
		} else if b < 0x20 {
			mask |= 0x02
		} else if b < 0x30 {
			mask |= 0x04
		}
	}
	if mask == 0x07 {
		return 3
	}
	if events != nil {
		for _, e := range events {
			switch e.GetColorType() {
			case 0x00:
				mask |= 0x01
			case 0x01:
				mask |= 0x02
			case 0x02:
				mask |= 0x04
			}
		}
	}
	log.Println("mask = ", mask)

	if mask == 0x01 || mask == 0x02 || mask == 0x04 {
		return int8(1)
	} else if mask == 0x03 || mask == 0x05 || mask == 0x06 {
		return int8(2)
	} else {
		return int8(3)
	}
}
