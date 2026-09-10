package model

// PracticeConfig is the saved configuration to send on the next explicit Apply.
// Overrides belong to team numbers, including teams not currently assigned.
type PracticeConfig struct {
	Id             int             `db:"id,manual" json:"-"`
	Revision       int             `json:"revision"`
	Stations       [6]int          `json:"stations"`
	CommonPassword string          `json:"commonPassword"`
	Overrides      map[int]string  `json:"overrides"`
	Network        PracticeNetwork `json:"network"`
}

type PracticeNetwork struct {
	NetworkSecurityEnabled bool   `json:"networkSecurityEnabled"`
	ApAddress              string `json:"apAddress"`
	ApPassword             string `json:"apPassword"`
	ApChannel              int    `json:"apChannel"`
	SwitchAddress          string `json:"switchAddress"`
	SwitchPassword         string `json:"switchPassword"`
}

func (database *Database) GetPracticeConfig() (*PracticeConfig, error) {
	config, err := database.practiceConfigTable.getById(1)
	if err != nil || config != nil {
		return config, err
	}
	config = &PracticeConfig{
		Id:        1,
		Overrides: map[int]string{},
		Network: PracticeNetwork{
			NetworkSecurityEnabled: true,
			ApAddress:              "10.0.100.2",
			ApChannel:              5,
			SwitchAddress:          "10.0.100.3",
		},
	}
	return config, database.practiceConfigTable.create(config)
}

func (database *Database) SavePracticeConfig(config *PracticeConfig) error {
	return database.practiceConfigTable.update(config)
}
