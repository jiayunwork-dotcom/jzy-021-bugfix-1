package cpm

// DemoNetwork is the built-in civil-engineering demonstration network.
//
//	(源)
//	 |
//	 A 场地清理            3d
//	 B 土方开挖            5d   (A)
//	 +-----------------------------+
//	 |                             |
//	 C1 地下雨水管线        4d (B) |   C2 基础浇筑    6d (B)
//	 |                             |
//	 +-------------+---------------+
//	               D 主体结构     8d  (C1, C2)   <- 汇合点 ES 取较大者 14
//	               E 竣工验收     2d  (D)
//	             (汇)
//
// Hand check (all durations deterministic, days):
//
//	path A-B-C1-D-E = 3+5+4+8+2 = 22
//	path A-B-C2-D-E = 3+5+6+8+2 = 24  <- project duration
//	C1 finishes early (EF=12 < C2 EF=14) yet keeps LS-ES = 2: a parallel
//	branch with positive total float is not critical.
//	A,B,C2,D,E: total float 0 -> unique critical path.
func DemoNetwork() NetworkInput {
	dur := func(d float64) *float64 { return &d }
	return NetworkInput{
		Name: "土建示范网络：办公楼基础与主体",
		Activities: []ActivityInput{
			{ID: "A", Duration: dur(3), Pred: nil},
			{ID: "B", Duration: dur(5), Pred: []string{"A"}},
			{ID: "C1", Duration: dur(4), Pred: []string{"B"}}, // 并行：雨水管线
			{ID: "C2", Duration: dur(6), Pred: []string{"B"}}, // 并行：基础浇筑
			{ID: "D", Duration: dur(8), Pred: []string{"C1", "C2"}},
			{ID: "E", Duration: dur(2), Pred: []string{"D"}},
		},
	}
}
