package chat

import (
	"hash/fnv"
	"strconv"
	"strings"
	"unicode"
)

// VoiceGender represents the grammatical/perceived vocal gender category of a voice or chatter.
type VoiceGender string

const (
	GenderUnknown   VoiceGender = ""
	GenderFeminine  VoiceGender = "f"
	GenderMasculine VoiceGender = "m"
	GenderNeutral   VoiceGender = "n"
)

// UnknownGenderPolicy controls which voice pool is used when a chatter's gender cannot be inferred.
const (
	UnknownGenderBalanced        = "balanced"
	UnknownGenderPreferFeminine  = "prefer_feminine"
	UnknownGenderPreferMasculine = "prefer_masculine"
	UnknownGenderDefaultVoice    = "default_voice"
)

// NormalizeUnknownGenderPolicy returns a canonical UnknownGenderPolicy value, defaulting to "balanced".
func NormalizeUnknownGenderPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case UnknownGenderPreferFeminine, "feminine", "female", "f":
		return UnknownGenderPreferFeminine
	case UnknownGenderPreferMasculine, "masculine", "male", "m":
		return UnknownGenderPreferMasculine
	case UnknownGenderDefaultVoice, "default":
		return UnknownGenderDefaultVoice
	default:
		return UnknownGenderBalanced
	}
}

// ClassifyVoiceGender classifies a TownCrier/Hearsay voice ID into Feminine ("f") or Masculine ("m").
func ClassifyVoiceGender(voiceID string) VoiceGender {
	id := strings.ToLower(strings.TrimSpace(voiceID))
	if openIdx := strings.LastIndexByte(id, '('); openIdx != -1 && strings.HasSuffix(id, ")") {
		extracted := strings.TrimSpace(id[openIdx+1 : len(id)-1])
		if extracted != "" {
			id = extracted
		}
	}
	if id == "" {
		return GenderFeminine
	}

	// Supertonic: supertonic:f1..f5 vs supertonic:m1..m5
	if after, ok := strings.CutPrefix(id, "supertonic:"); ok {
		if strings.HasPrefix(after, "f") {
			return GenderFeminine
		}
		if strings.HasPrefix(after, "m") {
			return GenderMasculine
		}
	}

	// Kitten Mini / Nano: kitten_mini:f1..f4 vs m1..m4
	if after, ok := strings.CutPrefix(id, "kitten_mini:"); ok {
		if strings.HasPrefix(after, "f") {
			return GenderFeminine
		}
		if strings.HasPrefix(after, "m") {
			return GenderMasculine
		}
	}
	if after, ok := strings.CutPrefix(id, "kitten_nano:"); ok {
		if strings.HasPrefix(after, "f") {
			return GenderFeminine
		}
		if strings.HasPrefix(after, "m") {
			return GenderMasculine
		}
	}

	// Piper LibriTTS: curated spk_0, spk_2, spk_4, spk_10 are Female; spk_1, spk_3, spk_5, spk_20 are Male
	if after, ok := strings.CutPrefix(id, "piper:"); ok {
		raw := strings.TrimPrefix(after, "spk_")
		if n, err := strconv.Atoi(raw); err == nil {
			if n == 20 || n%2 == 1 {
				return GenderMasculine
			}
			return GenderFeminine
		}
	}

	// Kokoro & Kokoro International: strip optional prefix
	id = strings.TrimPrefix(id, "kokoro_intl:")
	id = strings.TrimPrefix(id, "kokoro:")

	if id == "af" {
		return GenderFeminine
	}
	// Kokoro naming convention: <lang><gender>_<name>, e.g. af_bella, bf_emma, ef_dora, am_adam, bm_george, zm_yunxi
	if len(id) >= 3 && id[2] == '_' {
		switch id[1] {
		case 'f':
			return GenderFeminine
		case 'm':
			return GenderMasculine
		}
	}

	return GenderUnknown
}

// CanonicalVoicePoolKey normalizes a voice ID for pool inclusion/exclusion comparison.
func CanonicalVoicePoolKey(voiceID string) string {
	id := strings.ToLower(strings.TrimSpace(voiceID))
	id = strings.TrimPrefix(id, "kokoro:")
	return id
}

// FilterPermittedVoices returns the subset of availableVoices not present in disabledVoices.
func FilterPermittedVoices(availableVoices, disabledVoices []string) []string {
	if len(disabledVoices) == 0 || len(availableVoices) == 0 {
		return availableVoices
	}
	disabled := make(map[string]bool, len(disabledVoices))
	for _, d := range disabledVoices {
		if k := CanonicalVoicePoolKey(d); k != "" {
			disabled[k] = true
		}
	}
	if len(disabled) == 0 {
		return availableVoices
	}
	out := make([]string, 0, len(availableVoices))
	for _, v := range availableVoices {
		k := CanonicalVoicePoolKey(v)
		if k != "" && !disabled[k] {
			out = append(out, v)
		}
	}
	return out
}

// SplitVoicePoolByGender partitions a slice of voice IDs into feminine, masculine, and all non-empty voices.
func SplitVoicePoolByGender(voices []string) (feminine, masculine, all []string) {
	for _, v := range voices {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		all = append(all, trimmed)
		switch ClassifyVoiceGender(trimmed) {
		case GenderFeminine:
			feminine = append(feminine, trimmed)
		case GenderMasculine:
			masculine = append(masculine, trimmed)
		}
	}
	return feminine, masculine, all
}

// TokenizeUsername splits a handle or display name on camelCase boundaries, underscores,
// hyphens, spaces, punctuation, and digit transitions, returning lowercased alphabetic tokens.
func TokenizeUsername(name string) []string {
	s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@"))
	if s == "" {
		return nil
	}

	runes := []rune(s)
	var tokens []string
	var cur []rune

	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}

	for i, r := range runes {
		if !unicode.IsLetter(r) {
			flush()
			continue
		}
		if len(cur) > 0 {
			prev := runes[i-1]
			if unicode.IsLetter(prev) {
				// Lowercase -> Uppercase transition: e.g. "MrPound" -> "Mr", "Pound"
				if unicode.IsLower(prev) && unicode.IsUpper(r) {
					flush()
				} else if unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
					// Acronym -> Word transition: e.g. "TVSarah" -> "TV", "Sarah"
					flush()
				}
			}
		}
		cur = append(cur, r)
	}
	flush()
	return tokens
}

// InferUsernameGender analyzes a chatter's displayName and userName tokens for explicit
// honorifics, gendered role words, or unambiguous first names. Returns GenderUnknown when ambiguous.
func InferUsernameGender(displayName, userName string) VoiceGender {
	for _, candidate := range []string{displayName, userName} {
		if g := inferSingleNameGender(candidate); g != GenderUnknown {
			return g
		}
	}
	return GenderUnknown
}

func inferSingleNameGender(raw string) VoiceGender {
	tokens := TokenizeUsername(raw)
	if len(tokens) == 0 {
		return GenderUnknown
	}

	// Pass 1: Explicit honorifics, titles, and strong gendered keywords in any token
	for _, tok := range tokens {
		if g, ok := genderedKeywordMap[tok]; ok {
			return g
		}
	}

	// Pass 2: Check tokens (filtering out common stream noise tokens like "tv", "ttv", "gaming", "the")
	// against unambiguous given names.
	for _, tok := range tokens {
		if _, noise := streamNoiseTokens[tok]; noise {
			continue
		}
		if g, ok := givenNameGenderMap[tok]; ok {
			return g
		}
	}

	// Pass 3: Single-token all-lowercase concatenation (e.g. "mrsarah", "kingarthur", "sarahgaming")
	for _, tok := range tokens {
		if len(tok) < 5 {
			continue
		}
		for _, prefix := range explicitFemininePrefixes {
			if strings.HasPrefix(tok, prefix) && len(tok) > len(prefix)+1 {
				return GenderFeminine
			}
		}
		for _, prefix := range explicitMasculinePrefixes {
			if strings.HasPrefix(tok, prefix) && len(tok) > len(prefix)+1 {
				return GenderMasculine
			}
		}
		// Strip trailing stream suffix (e.g. "sarahgaming", "michaelplays", "emilytv")
		for _, suffix := range streamCommonSuffixes {
			if strings.HasSuffix(tok, suffix) && len(tok) > len(suffix)+2 {
				stem := tok[:len(tok)-len(suffix)]
				if g, ok := genderedKeywordMap[stem]; ok {
					return g
				}
				if g, ok := givenNameGenderMap[stem]; ok {
					return g
				}
			}
		}
	}

	return GenderUnknown
}

// SelectSmartChatterVoice deterministically selects a voice for a chatter without mutating any viewer config.
// When smartGender is enabled, it resolves the chatter's gender via pronouns -> username analysis -> unknownPolicy
// and hashes into the matching gender sub-pool.
func SelectSmartChatterVoice(
	platform, userName, displayName string,
	availableVoices []string,
	defaultVoice string,
	smartGender bool,
	unknownPolicy string,
	pronouns *PronounCache,
) string {
	femPool, mascPool, allPool := SplitVoicePoolByGender(availableVoices)
	if len(allPool) == 0 {
		if strings.TrimSpace(defaultVoice) != "" {
			return defaultVoice
		}
		return "supertonic:f1"
	}

	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(userName), "@"))
	if handle == "" {
		handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(displayName), "@"))
	}
	if handle == "" {
		if strings.TrimSpace(defaultVoice) != "" {
			return defaultVoice
		}
		return allPool[0]
	}

	h := hashChatterKey(platform, handle)
	if !smartGender {
		return allPool[h%uint32(len(allPool))]
	}

	gender := GenderUnknown
	if pronouns != nil {
		if g, ok := pronouns.LookupGender(platform, handle); ok {
			gender = g
		}
	}
	if gender == GenderUnknown {
		gender = InferUsernameGender(displayName, userName)
	}

	switch gender {
	case GenderFeminine:
		if len(femPool) > 0 {
			return femPool[h%uint32(len(femPool))]
		}
		return allPool[h%uint32(len(allPool))]
	case GenderMasculine:
		if len(mascPool) > 0 {
			return mascPool[h%uint32(len(mascPool))]
		}
		return allPool[h%uint32(len(allPool))]
	case GenderNeutral:
		return allPool[h%uint32(len(allPool))]
	default:
		switch NormalizeUnknownGenderPolicy(unknownPolicy) {
		case UnknownGenderPreferFeminine:
			if len(femPool) > 0 {
				return femPool[h%uint32(len(femPool))]
			}
		case UnknownGenderPreferMasculine:
			if len(mascPool) > 0 {
				return mascPool[h%uint32(len(mascPool))]
			}
		case UnknownGenderDefaultVoice:
			if strings.TrimSpace(defaultVoice) != "" {
				return defaultVoice
			}
		default:
			// UnknownGenderBalanced: exact 50/50 split between feminine and masculine voice pools
			if len(femPool) > 0 && len(mascPool) > 0 {
				idx := h >> 1
				if h&1 == 0 {
					return femPool[idx%uint32(len(femPool))]
				}
				return mascPool[idx%uint32(len(mascPool))]
			}
		}
		return allPool[h%uint32(len(allPool))]
	}
}

func hashChatterKey(platform, handle string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(platform))))
	_, _ = hasher.Write([]byte{':'})
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(handle))))
	return hasher.Sum32()
}

var explicitFemininePrefixes = []string{
	"mrs", "miss", "lady", "queen", "princess", "mama", "mommy", "sister", "goddess", "witch",
}

var explicitMasculinePrefixes = []string{
	"mister", "sir", "lord", "king", "prince", "papa", "daddy", "brother", "wizard",
}

var streamCommonSuffixes = []string{
	"gaming", "streams", "stream", "plays", "playz", "live", "official", "ttv", "yt", "tv", "xo", "xx",
}

var streamNoiseTokens = map[string]struct{}{
	"tv": {}, "ttv": {}, "yt": {}, "live": {}, "gaming": {}, "gamer": {},
	"plays": {}, "playz": {}, "streams": {}, "stream": {}, "official": {},
	"real": {}, "the": {}, "its": {}, "itz": {}, "im": {}, "iam": {},
	"xx": {}, "xxx": {}, "xo": {}, "xoxo": {}, "not": {}, "just": {},
}

var genderedKeywordMap = map[string]VoiceGender{
	// Masculine honorifics & role terms
	"mr": GenderMasculine, "mister": GenderMasculine, "sir": GenderMasculine, "sire": GenderMasculine,
	"lord": GenderMasculine, "king": GenderMasculine, "prince": GenderMasculine, "duke": GenderMasculine,
	"baron": GenderMasculine, "emperor": GenderMasculine, "tsar": GenderMasculine, "sultan": GenderMasculine,
	"boy": GenderMasculine, "boi": GenderMasculine, "boii": GenderMasculine, "ya_boi": GenderMasculine,
	"yaboi": GenderMasculine, "guy": GenderMasculine, "dude": GenderMasculine, "dudebro": GenderMasculine,
	"bro": GenderMasculine, "bruh": GenderMasculine, "brother": GenderMasculine, "brings": GenderMasculine,
	"dad": GenderMasculine, "daddy": GenderMasculine, "papa": GenderMasculine, "poppa": GenderMasculine,
	"father": GenderMasculine, "padre": GenderMasculine, "hubby": GenderMasculine, "husband": GenderMasculine,
	"son": GenderMasculine, "uncle": GenderMasculine, "grandpa": GenderMasculine, "gramps": GenderMasculine,
	"granddad": GenderMasculine, "man": GenderMasculine, "men": GenderMasculine, "gentleman": GenderMasculine,
	"gent": GenderMasculine, "lad": GenderMasculine, "fella": GenderMasculine, "chap": GenderMasculine,
	"bloke": GenderMasculine, "geezer": GenderMasculine, "beard": GenderMasculine, "bearded": GenderMasculine,
	"mustache": GenderMasculine, "moustache": GenderMasculine, "monk": GenderMasculine, "friar": GenderMasculine,
	"wizard": GenderMasculine, "warlock": GenderMasculine, "sorcerer": GenderMasculine, "knight": GenderMasculine,
	"cowboy": GenderMasculine, "fanboy": GenderMasculine, "catboy": GenderMasculine, "bachelor": GenderMasculine,
	"salesman": GenderMasculine, "businessman": GenderMasculine, "patriarch": GenderMasculine, "senor": GenderMasculine,
	"monsieur": GenderMasculine, "herr": GenderMasculine, "kun": GenderMasculine, "oniisan": GenderMasculine,
	"oniichan": GenderMasculine, "ojisan": GenderMasculine, "husbando": GenderMasculine,

	// Feminine honorifics & role terms
	"ms": GenderFeminine, "mrs": GenderFeminine, "miss": GenderFeminine, "missy": GenderFeminine,
	"mademoiselle": GenderFeminine, "madam": GenderFeminine, "madame": GenderFeminine, "maam": GenderFeminine,
	"lady": GenderFeminine, "queen": GenderFeminine, "princess": GenderFeminine, "duchess": GenderFeminine,
	"baroness": GenderFeminine, "countess": GenderFeminine, "empress": GenderFeminine, "goddess": GenderFeminine,
	"girl": GenderFeminine, "gurl": GenderFeminine, "grl": GenderFeminine, "girly": GenderFeminine,
	"gal": GenderFeminine, "lass": GenderFeminine, "lassie": GenderFeminine, "chick": GenderFeminine,
	"babe": GenderFeminine, "sis": GenderFeminine, "sista": GenderFeminine, "sister": GenderFeminine,
	"mom": GenderFeminine, "mommy": GenderFeminine, "mama": GenderFeminine, "mamma": GenderFeminine,
	"mum": GenderFeminine, "mummy": GenderFeminine, "mother": GenderFeminine, "madre": GenderFeminine,
	"wifey": GenderFeminine, "wife": GenderFeminine, "daughter": GenderFeminine, "aunt": GenderFeminine,
	"auntie": GenderFeminine, "grandma": GenderFeminine, "granny": GenderFeminine, "nana": GenderFeminine,
	"woman": GenderFeminine, "women": GenderFeminine, "female": GenderFeminine, "maiden": GenderFeminine,
	"damsel": GenderFeminine, "diva": GenderFeminine, "vixen": GenderFeminine, "witch": GenderFeminine,
	"witchy": GenderFeminine, "sorceress": GenderFeminine, "priestess": GenderFeminine, "enchantress": GenderFeminine,
	"heroine": GenderFeminine, "cowgirl": GenderFeminine, "fangirl": GenderFeminine, "catgirl": GenderFeminine,
	"egirl": GenderFeminine, "ballerina": GenderFeminine, "mermaid": GenderFeminine, "fairy": GenderFeminine,
	"pixie": GenderFeminine, "valkyrie": GenderFeminine, "bride": GenderFeminine, "widow": GenderFeminine,
	"matriarch": GenderFeminine, "senora": GenderFeminine, "senorita": GenderFeminine, "frau": GenderFeminine,
	"fraulein": GenderFeminine, "oneesan": GenderFeminine, "oneechan": GenderFeminine, "waifu": GenderFeminine,
}

var givenNameGenderMap = func() map[string]VoiceGender {
	m := make(map[string]VoiceGender, 700)
	femNames := []string{
		"aaliyah", "abby", "abigail", "ada", "addison", "adELAIDE", "adele", "adeline", "adriana", "adrienne",
		"agatha", "agnes", "aileen", "aimee", "aisling", "akari", "alana", "alanna", "alessandra", "alexa",
		"alexandra", "alexia", "alice", "alicia", "alina", "alison", "allison", "alma", "alyssa", "amanda",
		"amber", "amelia", "amelie", "amina", "amy", "ana", "anastasia", "andrea", "angela", "angelica",
		"angelina", "angie", "anita", "ann", "anna", "annabelle", "anne", "annette", "annie", "anya",
		"april", "aria", "ariana", "arianna", "ariel", "arlene", "ashley", "astrid", "athena", "aubrey",
		"audrey", "aurora", "autumn", "ava", "averie", "ayaka", "beatrice", "becky", "belinda", "bella",
		"bernadette", "bernice", "bertha", "bess", "beth", "bethany", "betsy", "betty", "beverly", "bianca",
		"blair", "blanche", "bonnie", "brandi", "brandy", "breanna", "brenda", "briana", "brianna", "bridGET",
		"brielle", "brigitte", "brittany", "brittney", "brooke", "brooklyn", "caitlin", "caitlyn", "camila", "camilla",
		"camille", "candace", "candice", "cara", "carina", "carla", "carly", "carmen", "carol", "carolina",
		"caroline", "carolyn", "carrie", "cassandra", "cassidy", "cassie", "catalina", "catherine", "cathleen", "cathy",
		"cecilia", "celeste", "celia", "celine", "chantal", "charlene", "charlotte", "charmaine", "chastity", "chelsea",
		"cherie", "cheryl", "chloe", "christa", "christina", "christine", "christy", "cindy", "claire", "clara",
		"clarissa", "claudia", "clementine", "colette", "colleen", "connie", "constance", "cora", "cordelia", "corinne",
		"courtney", "crystal", "cynthia", "daisy", "dakota", "dana", "daniela", "daniella", "danielle", "daphne",
		"darcy", "darlene", "dawn", "deanna", "debbie", "deborah", "debra", "deirdre", "delia", "delilah",
		"delores", "denise", "desiree", "destiny", "diana", "diane", "dianna", "dianne", "dina", "dolores",
		"dominique", "donna", "dora", "doreen", "doris", "dorothy", "edith", "edna", "eileen", "elaine",
		"eleanor", "elena", "eliana", "elisa", "elisabeth", "elise", "eliza", "elizabeth", "ella", "ellen",
		"ellie", "eloise", "elsa", "elsie", "elvira", "emilia", "emily", "emma", "emmaline", "ERICA",
		"erika", "erin", "esmeralda", "estelle", "esther", "ethel", "eugenia", "eunice", "eva", "evangeline",
		"eve", "evelyn", "everly", "faith", "fanny", "fatima", "faye", "felicia", "felicity", "fernanda",
		"fiona", "flora", "florence", "fran", "frances", "francesca", "francine", "freya", "frieda", "gabriela",
		"gabriella", "gabrielle", "gail", "gemma", "genevieve", "georgia", "georgina", "geraldine", "gertrude", "gia",
		"gianna", "gillian", "gina", "ginger", "giselle", "gladys", "glenda", "gloria", "grace", "gracie",
		"greta", "gretchen", "guadalupe", "gwen", "gwendolyn", "hailey", "haley", "hanna", "hannah", "haruka",
		"harmony", "harriet", "hattie", "hayley", "hazel", "heather", "heidi", "helen", "helena", "helene",
		"helga", "henrietta", "hilary", "hilda", "hillary", "hinata", "holly", "hope", "ida", "ilona",
		"imogen", "inez", "ingrid", "irene", "iris", "irma", "isabel", "isabella", "isabelle", "isla",
		"ivy", "jaclyn", "jacqueline", "jada", "jade", "jana", "jane", "janelle", "janet", "janice",
		"janie", "janine", "jasmine", "jayla", "jean", "jeanette", "jeanne", "jeannette", "jeannie", "jen",
		"jenna", "jennifer", "jenny", "jess", "jessica", "jessie", "jill", "jillian", "joan", "joann",
		"joanna", "joanne", "jocelyn", "jodi", "jodie", "jody", "jolene", "josephine", "joy", "joyce",
		"juana", "juanita", "judith", "judy", "julia", "juliana", "julianna", "julianne", "julie", "juliet",
		"juliette", "june", "justine", "kaitlin", "kaitlyn", "kara", "karen", "karina", "karla", "kate",
		"katelyn", "katerina", "katherine", "kathleen", "kathryn", "kathy", "katie", "katrina", "katya", "kay",
		"kayla", "kaylee", "keiko", "keira", "kelley", "kelli", "kellie", "kelly", "kelsey", "kendra",
		"khloe", "kiara", "kim", "kimberley", "kimberly", "kinsley", "kirsten", "krista", "kristen", "kristi",
		"kristin", "kristina", "kristine", "kristy", "krystal", "kylie", "kyra", "lacey", "lana", "lara",
		"larissa", "latoya", "laura", "laurel", "lauren", "laurie", "lavender", "layla", "lea", "leah",
		"leanna", "leanne", "leila", "leilani", "lena", "leona", "lesley", "leticia", "lexi", "lia",
		"liana", "libby", "lila", "lilian", "liliana", "lillian", "lillie", "lilly", "lily", "lina",
		"linda", "lindsay", "lindsey", "lisa", "livia", "liz", "liza", "lizzie", "lois", "lola",
		"lorelei", "lorena", "loretta", "lori", "lorna", "lorraine", "lottie", "louisa", "louise", "lucia",
		"luciana", "lucille", "lucinda", "lucy", "luisa", "luna", "lupe", "lydia", "lynda", "lynette",
		"lynn", "lynne", "mabel", "macey", "mackenzie", "macy", "madeline", "madelyn", "madison", "mae",
		"maeve", "magdalena", "maggie", "maia", "maisie", "makayla", "mallory", "mandy", "manuela", "mara",
		"marcella", "marcia", "marcie", "marcy", "margaret", "margarita", "marge", "margie", "margo", "margot",
		"marguerite", "maria", "mariah", "marian", "mariana", "marianne", "maribel", "marie", "marietta", "marilyn",
		"marina", "marisa", "marisol", "marissa", "marjorie", "marla", "marlene", "marsha", "martha", "martina",
		"mary", "maryann", "matilda", "maude", "maura", "maureen", "mavis", "maxine", "may", "maya",
		"mckenzie", "meagan", "megan", "meghan", "mei", "melanie", "melinda", "melissa", "melody", "mercedes",
		"meredith", "mia", "michaela", "michele", "michelle", "mika", "mila", "mildred", "millie", "mindy",
		"minerva", "minnie", "mira", "miranda", "miriam", "misaki", "misty", "mitzi", "moira", "molly",
		"mona", "monica", "monika", "monique", "mya", "myra", "myrtle", "nadia", "nadine", "nanami",
		"nancy", "nanette", "naomi", "natalia", "natalie", "natasha", "nevaeh", "nichole", "nicola", "nicole",
		"nicolette", "nikki", "nina", "noelle", "nola", "nora", "norah", "noreen", "norma", "nova",
		"OCTAVIA", "odette", "olga", "olive", "olivia", "opal", "ophelia", "paige", "paisley", "pam",
		"pamela", "pandora", "patricia", "patsy", "patti", "patty", "paula", "paulette", "pauline", "pearl",
		"peggy", "penelope", "penny", "petra", "phoebe", "phyllis", "piper", "polly", "poppy", "priscilla",
		"prudence", "rachael", "rachel", "rachelle", "raquel", "reagan", "rebecca", "rebekah", "regina", "rena",
		"renee", "rhianna", "rhonda", "ria", "rita", "roberta", "robyn", "rochelle", "rosa", "rosalie",
		"rosalind", "rosalyn", "rose", "rosemary", "rosetta", "rosie", "rowena", "roxanne", "ruby", "ruth",
		"ruthie", "sabINA", "sabrina", "sadie", "sakura", "sally", "salma", "samantha", "sandra", "sandy",
		"sara", "sarah", "sasha", "savannah", "scarlett", "selena", "selina", "selma", "serena", "serenity",
		"shANA", "shania", "shannon", "shari", "sharlene", "sharon", "shauna", "shawna", "sheena", "sheila",
		"shelby", "shelia", "shelley", "shelly", "sheri", "sherri", "sherrie", "sherry", "sheryl", "shirley",
		"sienna", "sierra", "silvia", "simone", "skye", "sofia", "sonia", "sonja", "sonya", "sophia",
		"sophie", "stacey", "staci", "stacie", "stacy", "stella", "steph", "stephanie", "sue", "summer",
		"susan", "susana", "susanna", "susanne", "suzanne", "suzie", "suzy", "svetlana", "sybil", "sydney",
		"sylvia", "sylvie", "tabitha", "talia", "tamara", "tameka", "tami", "tamika", "tammy", "tania",
		"tanya", "tara", "tasha", "tatiana", "tatum", "tessa", "thea", "thelma", "theresa", "therese",
		"tia", "tiana", "tiffany", "tina", "toni", "tonya", "tori", "tracey", "traci", "tracie",
		"tracy", "tricia", "trina", "trinity", "trish", "trisha", "trudy", "ursula", "valENTINA", "valerie",
		"vanessa", "vera", "veronica", "vicki", "vickie", "vicky", "victoria", "violet", "violeta", "virginia",
		"vivian", "viviana", "vivienne", "wanda", "wendi", "wendy", "whitney", "wilma", "winifred", "winnie",
		"winona", "ximena", "yasmin", "yasmine", "yesenia", "yolanda", "yumi", "yvonne", "zahra", "zara",
		"zelda", "zoe", "zoey", "zora",
	}

	mascNames := []string{
		"aaron", "abdullah", "abe", "abel", "abraham", "abram", "adam", "adan", "adelbert", "adolfo",
		"adolph", "adrian", "agustin", "ahmad", "ahmed", "aidan", "aiden", "akira", "al", "alan",
		"albert", "alberto", "alden", "aldo", "alec", "alejandro", "alessandro", "alex", "alexander", "alfonso",
		"alfred", "alfredo", "ali", "allan", "allen", "alonso", "alonzo", "alphonse", "alphonso", "alton",
		"alvaro", "alvin", "amadeo", "amos", "anders", "andre", "andreas", "andres", "andrew", "andy",
		"angelo", "angus", "ansel", "anthony", "antoine", "anton", "antonio", "antony", "apollo", "archie",
		"ari", "armand", "armando", "arnold", "aron", "art", "arthur", "arturo", "asa", "asher",
		"ashton", "atticus", "august", "augustine", "augustus", "austin", "axel", "barney", "barrett", "barry",
		"bart", "bartholomew", "basil", "beau", "ben", "benedict", "benito", "benjamin", "bennett", "bennie",
		"benny", "benson", "bentley", "bernard", "bernardo", "bernie", "bert", "bertram", "bill", "billy",
		"bjorn", "blaine", "blake", "bo", "bob", "bobby", "bodhi", "boris", "bowen", "boyd",
		"brad", "braden", "bradford", "bradley", "brady", "braeden", "branden", "brandon", "brant", "braxton",
		"brayden", "brendan", "brenden", "brendon", "brennan", "brent", "brenton", "bret", "brett", "brian",
		"brice", "brock", "brodie", "brody", "bronson", "brooks", "bruce", "bruno", "bryan", "bryant",
		"bryce", "bryson", "buck", "bud", "buddy", "buford", "burt", "burton", "buster", "byron",
		"cade", "caden", "caesar", "cal", "caleb", "callum", "calvin", "cameron", "carl", "carlo",
		"carlos", "carlton", "carmelo", "carmine", "carson", "carter", "cary", "casper", "cassius", "cecil",
		"cedric", "cesar", "chad", "chadwick", "chaim", "chance", "chandler", "charles", "charley", "charlie",
		"chase", "chauncey", "chester", "chet", "chris", "christian", "christopher", "chuck", "clarence", "clark",
		"claude", "claudio", "clay", "clayton", "clement", "cleveland", "cliff", "clifford", "clifton", "clint",
		"clinton", "clive", "clyde", "cody", "colby", "cole", "coleman", "colin", "collin", "colt",
		"colten", "colton", "conAN", "conner", "connor", "conor", "conrad", "constantine", "cooper", "corbin",
		"cordell", "corey", "cornelius", "corNELL", "cory", "craig", "cristian", "cristobal", "cruz", "cullen",
		"curt", "curtis", "cyril", "cyrus", "daiki", "dale", "dallas", "dalton", "damian", "damien",
		"damon", "dan", "dane", "daniel", "danny", "dante", "darell", "daren", "darian", "dario",
		"darius", "darnell", "darrel", "darrell", "darren", "darrin", "darryl", "daryl", "dave", "david",
		"davin", "davion", "davis", "dawson", "dax", "daxton", "dayton", "dean", "declan", "delbert",
		"demetrius", "denis", "dennis", "denny", "denver", "deon", "derek", "derick", "derrick", "desmond",
		"devin", "devon", "dewayne", "dewey", "dexter", "dick", "diego", "dillon", "dimitri", "dino",
		"dion", "dirk", "dom", "domingo", "dominic", "dominick", "dominik", "don", "donald", "donnie",
		"donny", "donovan", "dorian", "doug", "douglas", "oyle", "drake", "drew", "duane", "udley",
		"duncan", "dustin", "dusty", "duane", "dwight", "dylan", "earl", "earnest", "easton", "ed",
		"eddie", "eddy", "edgar", "edmond", "edmund", "eduardo", "edward", "edwardo", "edwin", "efrain",
		"efren", "elbert", "elden", "eldon", "eli", "elias", "elijah", "eliot", "elisha", "elliot",
		"elliott", "ellis", "elmer", "elmo", "eloy", "elton", "elvin", "elvis", "elwood", "emanuel",
		"emerson", "emery", "emil", "emile", "emiliano", "emilio", "emmanuel", "emmett", "emory", "enoch",
		"enrique", "enzo", "eric", "erich", "erick", "erik", "ernest", "ernesto", "ernie", "errol",
		"ervin", "erwin", "esteban", "ethan", "eugene", "evan", "everett", "ezequiel", "ezekiel", "ezra",
		"fabian", "federico", "felipe", "felix", "ferdinand", "fernando", "fidel", "finley", "finn", "finnegan",
		"fletcher", "floyd", "flynn", "forrest", "foster", "francis", "francisco", "franco", "frank", "frankie",
		"franklin", "fred", "freddie", "freddy", "frederic", "frederick", "fredrick", "fritz", "gabriel", "gael",
		"gage", "galen", "garland", "garrett", "garry", "garth", "gary", "gaston", "gavin", "gene",
		"geoff", "geoffrey", "george", "gerald", "gerard", "gerardo", "german", "gerry", "gideon", "MORT",
		"gil", "gilbert", "gilberto", "giles", "giovanni", "glen", "glenn", "gonzalo", "gordon", "grady",
		"graham", "grant", "grayson", "greg", "gregg", "gregorio", "gregory", "greyson", "griffin", "guillermo",
		"gunnar", "gunner", "gus", "gustav", "gustavo", "guy", "hal", "hamish", "hank", "hans",
		"harlan", "harley", "harold", "harrison", "harry", "haruto", "harvey", "hassan", "hayden", "heath",
		"hector", "hendrik", "henrik", "henry", "herb", "herbert", "heriberto", "herman", "hiram", "hiroshi",
		"holden", "homer", "horace", "houston", "howard", "howie", "hubert", "hudson", "hugh", "hugo",
		"humberto", "humphrey", "hunter", "ian", "ibrahim", "ignacio", "igor", "ike", "immanuel", "ira",
		"irvin", "irving", "irwin", "isaac", "isaiah", "isaias", "ishmael", "isiah", "isidore", "ismael",
		"israel", "issac", "ivan", "ivor", "izaiah", "jabari", "jace", "jack", "jackson", "jacob",
		"jacques", "jaden", "jadon", "jaime", "jair", "jake", "jakob", "jalen", "jamal", "jamar",
		"james", "jameson", "jamison", "jan", "jared", "jarod", "jarrett", "jarrod", "jarvis", "jase",
		"jason", "jasper", "javier", "javon", "jax", "jaxon", "jaxson", "jay", "jayce", "jayden",
		"jaylen", "jayson", "jed", "jedidiah", "jeff", "jefferson", "jeffery", "jeffrey", "jensen", "jeremiah",
		"jeremy", "jermaine", "jerome", "jeromy", "jerrad", "jerrod", "jerry", "jesper", "jess", "jesse",
		"jesus", "jett", "jim", "jimmie", "jimmy", "joaquin", "jock", "joe", "joel", "joesph",
		"joey", "johan", "johann", "johannes", "john", "johnathan", "johnathon", "johnnie", "johnny", "jon",
		"jonah", "jonas", "jonathan", "jonathon", "jordan", "jordi", "jordon", "jorge", "jose", "josef",
		"joseph", "josh", "joshua", "josiah", "josue", "jovan", "juan", "judah", "jude", "judson",
		"jules", "julian", "julien", "julio", "julius", "junior", "justin", "justus", "kade", "kaden",
		"kai", "kaiden", "kaito", "kaleb", "kameron", "kamron", "kane", "kareem", "karl", "karson",
		"kasper", "keanu", "keaton", "keegan", "keenan", "keith", "kellan", "kellen", "kelvin", "ken",
		"kendall", "kendrick", "kenji", "kennedy", "kenneth", "kenny", "kent", "kenton", "kermit", "kevin",
		"khalid", "khalil", "kian", "kieran", "killian", "kingston", "kip", "kirby", "kirk", "klaus",
		"knox", "kobe", "koby", "kody", "kolby", "kole", "konnor", "korbin", "kory", "kris",
		"kristian", "kristofer", "kristoffer", "kristopher", "kurt", "kurtis", "kyle", "kyler", "kyree", "lamar",
		"lamont", "lance", "landen", "landon", "landyn", "lane", "larry", "lars", "laurence", "lawrence",
		"lawson", "layne", "layton", "lazaro", "leander", "lee", "leif", "leland", "lemuel", "len",
		"lenard", "lennon", "lenny", "leo", "leon", "leonard", "leonardo", "leonel", "leonidas", "leopold",
		"leroy", "les", "lester", "levi", "lewis", "liam", "lincoln", "lionel", "lloyd", "logan",
		"lonnie", "loren", "lorenzo", "lou", "louie", "louis", "lowell", "loyd", "luca", "lucas",
		"lucian", "lucien", "lucio", "lucius", "luigi", "luis", "luka", "lukas", "luke", "luther",
		"lyle", "lyman", "lyndon", "mac", "mack", "madden", "maddox", "magnus", "major", "malachi",
		"malakai", "malcolm", "malik", "manny", "manuel", "marc", "marcel", "marcelino", "marcellus", "marcelo",
		"marco", "marcos", "marcus", "mariano", "mario", "marion", "marius", "mark", "markus", "marlin",
		"marlon", "marquis", "marshall", "martin", "marty", "marvin", "mason", "mateo", "mathew", "mathias",
		"matt", "matteo", "matthew", "matthias", "maurice", "mauricio", "mauro", "maverick", "max", "maxim",
		"maximilian", "maximiliano", "maximo", "maximus", "maxwell", "maynard", "mekhi", "mel", "melvin", "memphis",
		"merle", "merlin", "merrill", "mervin", "micah", "michael", "michal", "micheal", "michel", "mickey",
		"miguel", "mike", "mikel", "milan", "miles", "milford", "millard", "milo", "milton", "mitch",
		"mitchel", "mitchell", "mohamed", "mohammad", "mohammed", "moises", "monte", "montgomery", "monty", "morris",
		"morton", "moses", "moshe", "muhammad", "murray", "mustafa", "myles", "myron", "nash", "nasir",
		"uss", "nate", "nathan", "nathanael", "nathaniel", "neal", "ned", "nehemiah", "neil", "nelson",
		"nestor", "neville", "newton", "nicholas", "nick", "nickolas", "nicky", "nico", "nicolas", "nigel",
		"nikita", "nikolai", "nikolas", "niles", "nils", "noah", "noe", "noel", "nolan", "norm",
		"norman", "norris", "norton", "odin", "olaf", "oliver", "ollie", "omar", "omari", "orion",
		"orlando", "orville", "osbaldo", "oscar", "osvaldo", "oswald", "otis", "otto", "owen", "pablo",
		"paco", "padraig", "palmer", "paolo", "parker", "pascal", "pat", "patrick", "paul", "paulo",
		"pavel", "paxton", "pedro", "percy", "perry", "pete", "peter", "peyton", "phil", "philip",
		"phillip", "phoenix", "pierce", "pierre", "porter", "preston", "prince", "quentin", "quincy", "quinton",
		"rafael", "rafe", "ragnar", "ralph", "ramiro", "ramon", "ramsey", "randal", "randall", "randolph",
		"randy", "raphael", "rashad", "raul", "ray", "raymond", "raymundo", "reed", "reese", "reggie",
		"reginald", "reid", "reinaldo", "remington", "remy", "ren", "rene", "reuben", "rex", "rey",
		"reynaldo", "rhett", "ricardo", "rich", "richard", "richie", "rick", "rickey", "rickie", "ricky",
		"rico", "ridge", "rigoberto", "riku", "riley", "river", "rob", "robbie", "robby", "robert",
		"roberto", "occo", "rocky", "rod", "roderick", "rodger", "rodney", "rodolfo", "rodrigo", "rogelio",
		"roger", "rohan", "roland", "rolando", "roman", "romeo", "ron", "ronald", "ronan", "ronnie",
		"ronny", "roosevelt", "rory", "roscoe", "ross", "rowan", "roy", "royal", "royce", "ruben",
		"rudolf", "rudolph", "rudy", "rufus", "rupert", "russ", "russel", "russell", "rusty", "ryan",
		"ryder", "ryker", "rylan", "ryland", "sal", "salvador", "salvatore", "sam", "samir", "sammie",
		"sammy", "samson", "samuel", "santiago", "santino", "santos", "saul", "sawyer", "scot", "scott",
		"scottie", "scotty", "seamUS", "sean", "sebastian", "sergio", "seth", "seymour", "shane", "shannon",
		"shaquille", "shaun", "shawn", "shay", "shayne", "shea", "sheldon", "shelton", "sherman", "sherwin",
		"sid", "sidney", "silas", "silvester", "simeon", "simon", "sol", "solomon", "sonny", "soren",
		"sota", "spencer", "stan", "stanford", "stanley", "stefan", "stephan", "stephen", "sterling", "steve",
		"steven", "stevie", "stewart", "stone", "stuart", "stu", "sullivan", "sylvester", "tad", "takashi",
		"takeshi", "talon", "tanner", "tariq", "tate", "ted", "teddy", "terence", "terrance", "terrell",
		"terrence", "terry", "thaddeus", "theo", "theodore", "theron", "thomas", "thor", "thurman", "tim",
		"timmy", "timothy", "tito", "titus", "tobias", "toby", "tod", "todd", "tom", "tomas",
		"tommie", "tommy", "tony", "trace", "travis", "trent", "trenton", "trevor", "trey", "tristan",
		"tristen", "tristian", "triston", "troy", "truman", "tucker", "ty", "tyler", "tyree", "tyrell",
		"tyrone", "tyson", "ulises", "ulysses", "uri", "uriah", "uriel", "valentin", "valentino", "van",
		"vance", "vaughn", "vern", "vernon", "vic", "vicente", "victor", "vince", "vincent", "vincenzo",
		"vinny", "virgil", "vito", "vladimir", "wade", "waldo", "walker", "wallace", "wally", "walt",
		"walter", "ward", "warren", "waylon", "wayne", "weldon", "wendell", "werner", "wes", "wesley",
		"weston", "wilbert", "wilbur", "wilder", "wiley", "wilford", "wilfred", "wilfredo", "will", "willard",
		"william", "williams", "willie", "willis", "willy", "wilmer", "wilson", "wilton", "winston", "wm",
		"wolfgang", "woodrow", "woody", "wyatt", "xander", "xavier", "yahir", "yosef", "yuri", "yusuf",
		"yuto", "zach", "zachariah", "zachary", "zachery", "zack", "zackary", "zackery", "zaid", "zain",
		"zane", "zander", "zavier", "zayne", "zebulon", "zeke", "zion",
	}

	for _, n := range femNames {
		m[strings.ToLower(n)] = GenderFeminine
	}
	for _, n := range mascNames {
		lower := strings.ToLower(n)
		if _, exists := m[lower]; exists {
			// If a name appeared in both lists, remove it so unisex names remain GenderUnknown
			delete(m, lower)
			continue
		}
		m[lower] = GenderMasculine
	}
	return m
}()
