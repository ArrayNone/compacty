package textutils

func PluralNoun(count int, plural, singular string) string {
	if count == 1 {
		return singular
	}

	return plural
}
