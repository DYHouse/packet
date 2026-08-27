package robot

import (
	"fmt"
	"math/rand"
)

// 英文名音节拼装生成器。
// 昵称结构：首音节 + [中音节] + 尾音节 + [随机后缀]，无查重依赖。
// 音节常量表为英语常用名片段，产出可发音的英文名风格昵称。

// onsetSyllables 首音节集合：英语常用名开头。
var onsetSyllables = []string{
	"James", "John", "Robert", "Michael", "William", "David", "Richard", "Joseph",
	"Thomas", "Charles", "Christopher", "Daniel", "Matthew", "Anthony", "Mark",
	"Donald", "Steven", "Paul", "Andrew", "Joshua", "Kenneth", "Kevin", "Brian",
	"George", "Timothy", "Ronald", "Edward", "Jason", "Jeffrey", "Ryan", "Jacob",
	"Gary", "Nicholas", "Eric", "Jonathan", "Stephen", "Larry", "Justin", "Scott",
	"Frank", "Benjamin", "Samuel", "Gregory", "Raymond", "Alexander", "Patrick",
	"Jack", "Dennis", "Jerry", "Tyler", "Aaron", "Jose", "Adam", "Henry",
	"Nathan", "Douglas", "Zachary", "Peter", "Kyle", "Walter", "Harold", "Jeremy",
	"Ethan", "Carl", "Keith", "Roger", "Gerald", "Terry", "Sean", "Austin",
	"Christian", "Connor", "Jake", "Tracey", "Bob", "Leo", "Alex", "Max",
}

// middleSyllables 中音节集合（可选，约 40% 概率插入）。
var middleSyllables = []string{
	"son", "ton", "der", "van", "ter", "rin", "den", "ven",
	"ford", "ley", "ley", "son", "ian", "ard", "rick", "man",
	"ston", "drew", "land", "ret", "cott", "bert", "ley",
}

// tailSyllables 尾音节集合：英语常用名结尾。
var tailSyllables = []string{
	"s", "son", "er", "ley", "ard", "ton", "sen", "thy",
	"ick", "ett", "in", "or", "is", "ean", "eus", "ian",
	"ael", "us", "er", "an", "on", "as", "en", "", "", "",
}

// suffixPatterns 后缀风格（50% 概率追加，随机选一种）。
// 空字符串用于产生"无后缀"情况。
var suffixPatterns = []string{
	"", "", "", "", // 40% 无后缀
	"_", "x", "", "", "",
}

// suffixStylesSuffixes 对应后缀前缀后拼接的内容模板。
// _ 风格拼 2 位数字，x 风格拼 XX 包裹（xNicknameX）。
// 枚举不同后缀效果：James_92、Emily7、xAlexx、Daniel03。
var suffixNumericDigits = []string{
	"1", "2", "3", "4", "5", "6", "7", "8", "9", "0",
	"01", "02", "03", "04", "05", "06", "07", "08", "09", "10",
	"11", "23", "42", "77", "88", "92", "99", "00", "07",
}

// GenerateNickname 生成一个英语风格随机昵称。
// 纯函数，无外部依赖，并发安全（使用 rand.Intn 做索引选择；
// 随机数来源 math/rand，符合非金额/非安全场景使用规范）。
func GenerateNickname() string {
	onset := onsetSyllables[rand.Intn(len(onsetSyllables))]
	middle := ""
	if rand.Intn(10) < 4 {
		middle = middleSyllables[rand.Intn(len(middleSyllables))]
	}
	tail := tailSyllables[rand.Intn(len(tailSyllables))]

	base := onset + middle + tail

	// 后缀：50% 概率追加
	if rand.Intn(10) < 5 {
		style := rand.Intn(3)
		switch style {
		case 0:
			// James_92
			suffix := suffixNumericDigits[rand.Intn(len(suffixNumericDigits))]
			return fmt.Sprintf("%s_%s", base, suffix)
		case 1:
			// Emily7
			suffix := suffixNumericDigits[rand.Intn(len(suffixNumericDigits))]
			return fmt.Sprintf("%s%s", base, suffix)
		case 2:
			// xAlexx
			return fmt.Sprintf("x%sx", base)
		}
	}

	return base
}
