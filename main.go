package main

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    "github.com/gdamore/tcell/v2"
    "github.com/rivo/tview"
    "github.com/spf13/cobra"
)

const (
	CacheDir          = ".weather-cli-cache"
	ConfigFile        = "config.json"
	LocationCacheFile = "locations.json"
	WeatherCacheFile  = "weather_cache.json"
)

const (
	NominatimURL = "https://nominatim.openstreetmap.org/search"
)

var weatherIcons = map[string]string{
	"sunny":         "☀️ ",
	"clear":         "☀️ ",
	"mostly clear":  "🌤 ",
	"partly sunny":  "⛅️",
	"partly cloudy": "⛅️",
	"mostly cloudy": "🌥 ",
	"cloudy":        "☁️ ",
	"rain":          "🌧 ",
	"showers":       "🌦 ",
	"thunderstorm":  "⛈ ",
	"snow":          "❄️ ",
	"sleet":         "🌨 ",
	"windy":         "💨 ",
	"fog":           "🌫 ",
	"haze":          "🌫 ",
}

type PointsResponse struct {
	Properties struct {
		GridId           string `json:"gridId"`
		GridX            int    `json:"gridX"`
		GridY            int    `json:"gridY"`
		RelativeLocation struct {
			Properties struct {
				City    string `json:"city"`
				State   string `json:"state"`
				Country string `json:"country"`
			} `json:"properties"`
		} `json:"relativeLocation"`
		Forecast         string `json:"forecast"`
		ForecastHourly   string `json:"forecastHourly"`
		ForecastGridData string `json:"forecastGridData"`
	} `json:"properties"`
}

type ForecastResponse struct {
	Properties struct {
		Updated     string   `json:"updated"`
		GeneratedAt string   `json:"generatedAt"`
		Periods     []Period `json:"periods"`
	} `json:"properties"`
}

type Period struct {
	Number           int    `json:"number"`
	Name             string `json:"name"`
	StartTime        string `json:"startTime"`
	EndTime          string `json:"endTime"`
	Temperature      int    `json:"temperature"`
	TemperatureUnit  string `json:"temperatureUnit"`
	WindSpeed        string `json:"windSpeed"`
	WindDirection    string `json:"windDirection"`
	ShortForecast    string `json:"shortForecast"`
	DetailedForecast string `json:"detailedForecast"`
	IsDaytime        bool   `json:"isDaytime"`
}

type GeocodingResult struct {
	Lat         string `json:"lat"`
	Lon         string `json:"lon"`
	DisplayName string `json:"display_name"`
}

type Config struct {
	FavoriteLocations []FavoriteLocation `json:"favoriteLocations"`
}

type FavoriteLocation struct {
	Name      string `json:"name"`
	Latitude  string `json:"latitude"`
	Longitude string `json:"longitude"`
}

type CacheEntry struct {
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
	Expiry    time.Time   `json:"expiry"`
}

type Cache map[string]CacheEntry

func main() {
	setupCache()

	var rootCmd = &cobra.Command{
		Use:   "weather",
		Short: "A weather CLI with enhanced features",
		Long:  `A feature-rich weather CLI application that uses the National Weather Service API.`,
	}

	rootCmd.AddCommand(getCmd())
	rootCmd.AddCommand(locationCmd())
	rootCmd.AddCommand(uiCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func setupCache() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Warning: Could not determine home directory: %s\n", err)
		return
	}

	cacheDir := filepath.Join(homeDir, CacheDir)
	if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
		if err := os.Mkdir(cacheDir, 0755); err != nil {
			fmt.Printf("Warning: Could not create cache directory: %s\n", err)
		}
	}
}

func getCmd() *cobra.Command {
	var location, coordinates string
	var showDetailedForecast, showHourly bool
	var date string

	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get weather information",
		Run: func(cmd *cobra.Command, args []string) {
			lat, long := "", ""
			var err error

			if location != "" {
				config := loadConfig()
				for _, favLoc := range config.FavoriteLocations {
					if strings.EqualFold(favLoc.Name, location) {
						lat, long = favLoc.Latitude, favLoc.Longitude
						fmt.Printf("Using favorite location: %s (%s,%s)\n", favLoc.Name, lat, long)
						break
					}
				}

				if lat == "" || long == "" {
					fmt.Printf("Geocoding location: %s\n", location)
					lat, long, err = geocodeLocation(location)
					if err != nil {
						fmt.Printf("Error geocoding location: %s\n", err)
						os.Exit(1)
					}
					fmt.Printf("Found coordinates: %s,%s\n", lat, long)
				}
			} else if coordinates != "" {
				lat, long, err = parseCoordinates(coordinates)
				if err != nil {
					fmt.Printf("Error parsing coordinates: %s\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Println("Error: Either --location or --coordinates must be provided")
				os.Exit(1)
			}

			pointsCacheKey := fmt.Sprintf("points_%s_%s", lat, long)
			var points *PointsResponse

			cachedPoints, found := getFromCache(pointsCacheKey)
			if found {
				points = cachedPoints.(*PointsResponse)
				fmt.Println("Using cached location data")
			} else {
				var err error
				points, err = getPoints(lat, long)
				if err != nil {
					fmt.Printf("Error fetching location data: %s\n", err)
					os.Exit(1)
				}
				saveToCache(pointsCacheKey, points, 24*time.Hour)
			}

			if showHourly {
				hourlyCacheKey := fmt.Sprintf("hourly_%s_%s", lat, long)

				cachedForecast, found := getFromCache(hourlyCacheKey)
				if found {
					forecast := cachedForecast.(*ForecastResponse)
					fmt.Println("Using cached hourly forecast data")
					displayWeather(points, forecast, showDetailedForecast, true)
				} else {
					forecast, err := getForecast(points.Properties.ForecastHourly)
					if err != nil {
						fmt.Printf("Error fetching hourly forecast: %s\n", err)
						os.Exit(1)
					}
					saveToCache(hourlyCacheKey, forecast, 1*time.Hour) 
					displayWeather(points, forecast, showDetailedForecast, true)
				}
			} else {
				forecastCacheKey := fmt.Sprintf("forecast_%s_%s", lat, long)

				cachedForecast, found := getFromCache(forecastCacheKey)
				if found {
					forecast := cachedForecast.(*ForecastResponse)
					fmt.Println("Using cached forecast data")

					if date != "" {
						filterAndDisplayForecastByDate(points, forecast, date)
					} else {
						displayWeather(points, forecast, showDetailedForecast, false)
					}
				} else {
					forecast, err := getForecast(points.Properties.Forecast)
					if err != nil {
						fmt.Printf("Error fetching forecast data: %s\n", err)
						os.Exit(1)
					}
					saveToCache(forecastCacheKey, forecast, 1*time.Hour) 

					if date != "" {
						filterAndDisplayForecastByDate(points, forecast, date)
					} else {
						displayWeather(points, forecast, showDetailedForecast, false)
					}
				}
			}
		},
	}

	cmd.Flags().StringVarP(&location, "location", "l", "", "Location name (city, address, etc.)")
	cmd.Flags().StringVarP(&coordinates, "coordinates", "c", "", "Coordinates in the format latitude,longitude")
	cmd.Flags().BoolVarP(&showDetailedForecast, "detailed", "d", false, "Show detailed forecast")
	cmd.Flags().BoolVarP(&showHourly, "hourly", "H", false, "Show hourly forecast")
	cmd.Flags().StringVarP(&date, "date", "D", "", "Show forecast for specific date (YYYY-MM-DD)")

	cmd.MarkFlagRequired("location")
	cmd.MarkFlagsMutuallyExclusive("location", "coordinates")

	return cmd
}

func locationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "location",
		Short: "Manage favorite locations",
	}

	cmd.AddCommand(locationAddCmd())
	cmd.AddCommand(locationListCmd())
	cmd.AddCommand(locationRemoveCmd())

	return cmd
}

func locationAddCmd() *cobra.Command {
	var name, coordinates, address string

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a favorite location",
		Run: func(cmd *cobra.Command, args []string) {
			if name == "" {
				fmt.Println("Error: Location name is required")
				os.Exit(1)
			}

			var lat, long string
			var err error

			if coordinates != "" {
				lat, long, err = parseCoordinates(coordinates)
				if err != nil {
					fmt.Printf("Error parsing coordinates: %s\n", err)
					os.Exit(1)
				}
			} else if address != "" {
				fmt.Printf("Geocoding address: %s\n", address)
				lat, long, err = geocodeLocation(address)
				if err != nil {
					fmt.Printf("Error geocoding address: %s\n", err)
					os.Exit(1)
				}
				fmt.Printf("Found coordinates: %s,%s\n", lat, long)
			} else {
				fmt.Println("Error: Either --coordinates or --address must be provided")
				os.Exit(1)
			}

			config := loadConfig()

			for i, loc := range config.FavoriteLocations {
				if strings.EqualFold(loc.Name, name) {
					config.FavoriteLocations[i].Latitude = lat
					config.FavoriteLocations[i].Longitude = long
					saveConfig(config)
					fmt.Printf("Updated favorite location: %s (%s,%s)\n", name, lat, long)
					return
				}
			}

			config.FavoriteLocations = append(config.FavoriteLocations, FavoriteLocation{
				Name:      name,
				Latitude:  lat,
				Longitude: long,
			})
			saveConfig(config)
			fmt.Printf("Added favorite location: %s (%s,%s)\n", name, lat, long)
		},
	}

	cmd.Flags().StringVarP(&name, "name", "n", "", "Name for this location")
	cmd.Flags().StringVarP(&coordinates, "coordinates", "c", "", "Coordinates in the format latitude,longitude")
	cmd.Flags().StringVarP(&address, "address", "a", "", "Address to geocode")
	cmd.MarkFlagRequired("name")

	return cmd
}

func locationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List favorite locations",
		Run: func(cmd *cobra.Command, args []string) {
			config := loadConfig()
			if len(config.FavoriteLocations) == 0 {
				fmt.Println("No favorite locations saved.")
				return
			}

			fmt.Println("Favorite Locations:")
			fmt.Println("--------------------")
			for _, loc := range config.FavoriteLocations {
				fmt.Printf("%s: %s,%s\n", loc.Name, loc.Latitude, loc.Longitude)
			}
		},
	}
}

func locationRemoveCmd() *cobra.Command {
    var name string

    cmd := &cobra.Command{
        Use:   "remove",
        Short: "Remove a favorite location",
        Run: func(cmd *cobra.Command, args []string) {
            if name == "" {
                fmt.Println("Error: Location name is required")
                os.Exit(1)
            }

            config := loadConfig()
            found := false

            for i, loc := range config.FavoriteLocations {
                if strings.EqualFold(loc.Name, name) {
                    config.FavoriteLocations = append(config.FavoriteLocations[:i], config.FavoriteLocations[i+1:]...)
                    saveConfig(config)
                    fmt.Printf("Removed favorite location: %s\n", name)
                    found = true
                    break
                }
            }

            if !found {
                fmt.Printf("Location '%s' not found in favorites\n", name)
            }
        },
    }

    cmd.Flags().StringVarP(&name, "name", "n", "", "Name of location to remove")
    cmd.MarkFlagRequired("name")

    return cmd
}

func uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Launch interactive terminal UI",
		Run: func(cmd *cobra.Command, args []string) {
			launchTerminalUI()
		},
	}
}

func launchTerminalUI() {
    app := tview.NewApplication()

    flex := tview.NewFlex().SetDirection(tview.FlexRow)

    titleBar := tview.NewTextView().
        SetTextAlign(tview.AlignCenter).
        SetText("Weather CLI").
        SetTextColor(tcell.ColorYellow)

    // Properly declare as *tview.List and initialize
    locationList := tview.NewList()
    locationList.SetTitle("Favorite Locations").
        SetTitleAlign(tview.AlignLeft).
		SetBorder(true)

	weatherView := tview.NewTextView() // Initialize first
	weatherView.SetTitle("Weather Information"). // Then configure
		SetTitleAlign(tview.AlignLeft).
		SetBorder(true)

	statusBar := tview.NewTextView().
		SetText("Press Ctrl-C to quit | Arrow keys to navigate | Enter to select").
		SetTextColor(tcell.ColorGreen)

    flex.AddItem(titleBar, 1, 1, false).
        AddItem(tview.NewFlex().
            AddItem(locationList, 0, 1, true).
            AddItem(weatherView, 0, 3, false),
            0, 1, true).
        AddItem(statusBar, 1, 1, false)

    config := loadConfig()

    locationList.AddItem("Add New Location...", "", 'a', nil)
    locationList.AddItem("Refresh Current Weather", "", 'r', nil)

    // Rest of the code remains the same...
    for _, loc := range config.FavoriteLocations {
        locationName := loc.Name
        lat, long := loc.Latitude, loc.Longitude

        locationList.AddItem(locationName, fmt.Sprintf("%s,%s", lat, long), 0, func() {
            weatherView.SetText("Loading weather data...")
            go func() {
                points, err := getPoints(lat, long)
                if err != nil {
                    app.QueueUpdateDraw(func() {
                        weatherView.SetText(fmt.Sprintf("Error: %s", err))
                    })
                    return
                }

                forecast, err := getForecast(points.Properties.Forecast)
                if err != nil {
                    app.QueueUpdateDraw(func() {
                        weatherView.SetText(fmt.Sprintf("Error: %s", err))
                    })
                    return
                }

                app.QueueUpdateDraw(func() {
                    weatherText := formatWeatherForTUI(points, forecast)
                    weatherView.SetText(weatherText)
                })
            }()
        })
    }

    // Rest of the function remains the same...
    if err := app.SetRoot(flex, true).EnableMouse(true).Run(); err != nil {
        fmt.Printf("Error running terminal UI: %s\n", err)
        os.Exit(1)
    }
}

func formatWeatherForTUI(points *PointsResponse, forecast *ForecastResponse) string {
	var sb strings.Builder

	location := points.Properties.RelativeLocation.Properties
	sb.WriteString(fmt.Sprintf("[yellow]Weather for %s, %s[white]\n", location.City, location.State))
	sb.WriteString("------------------------------\n")
	sb.WriteString(fmt.Sprintf("Last updated: %s\n", forecast.Properties.Updated))

	if len(forecast.Properties.Periods) > 0 {
		current := forecast.Properties.Periods[0]

		icon := getWeatherIcon(current.ShortForecast)

		sb.WriteString("------------------------------\n")
		sb.WriteString(fmt.Sprintf("[::b]%s: %s %s[::]\n", current.Name, icon, current.ShortForecast))
		sb.WriteString(fmt.Sprintf("[cyan]Temperature:[white] %d°%s\n", current.Temperature, current.TemperatureUnit))
		sb.WriteString(fmt.Sprintf("[cyan]Wind:[white] %s %s\n", current.WindSpeed, current.WindDirection))
		sb.WriteString(fmt.Sprintf("\n[green]Detailed forecast:[white] %s\n", current.DetailedForecast))

		sb.WriteString("\n[yellow]Extended Forecast:[white]\n")
		sb.WriteString("------------------------------\n")

		for i := 1; i < len(forecast.Properties.Periods) && i < 6; i++ {
			period := forecast.Properties.Periods[i]
			icon := getWeatherIcon(period.ShortForecast)

			sb.WriteString(fmt.Sprintf("\n[::b]%s:[::] %s %s\n", period.Name, icon, period.ShortForecast))
			sb.WriteString(fmt.Sprintf("Temperature: %d°%s, Wind: %s %s\n",
				period.Temperature, period.TemperatureUnit, period.WindSpeed, period.WindDirection))
		}
	}

	return sb.String()
}

func getWeatherIcon(forecast string) string {
	forecast = strings.ToLower(forecast)

	for condition, icon := range weatherIcons {
		if strings.Contains(forecast, condition) {
			return icon
		}
	}

	return "🌡️ "
}

func parseCoordinates(coords string) (string, string, error) {
    parts := strings.Split(coords, ",")
    if len(parts) != 2 {
        return "", "", fmt.Errorf("coordinates must be in the format latitude,longitude")
    }

    lat := strings.TrimSpace(parts[0])
    long := strings.TrimSpace(parts[1])

    // Validate latitude and longitude values
    latF, err := strconv.ParseFloat(lat, 64)
    if err != nil || latF < -90 || latF > 90 {
        return "", "", fmt.Errorf("invalid latitude: must be between -90 and 90")
    }

    longF, err := strconv.ParseFloat(long, 64)
    if err != nil || longF < -180 || longF > 180 {
        return "", "", fmt.Errorf("invalid longitude: must be between -180 and 180")
    }

    return lat, long, nil
}

func geocodeLocation(location string) (string, string, error) {
	cacheKey := fmt.Sprintf("geocode_%s", location)

	cached, found := getFromCache(cacheKey)
	if found {
		result := cached.(GeocodingResult)
		return result.Lat, result.Lon, nil
	}

	url := fmt.Sprintf("%s?q=%s&format=json&limit=1", NominatimURL, url.QueryEscape(location))

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", "", err
	}

	req.Header.Set("User-Agent", "GoWeatherCLI/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("Geocoding API error: %s - %s", resp.Status, string(body))
	}

	var results []GeocodingResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return "", "", err
	}

	if len(results) == 0 {
		return "", "", fmt.Errorf("no results found for location: %s", location)
	}

	saveToCache(cacheKey, results[0], 7*24*time.Hour) 

	return results[0].Lat, results[0].Lon, nil
}

func getPoints(lat, long string) (*PointsResponse, error) {
	url := fmt.Sprintf("https://api.weather.gov/points/%s,%s", lat, long)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "GoWeatherCLI/1.0 (github.com/yourusername/go-weather-cli)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(body))
	}

	var data PointsResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return &data, nil
}

func getForecast(forecastURL string) (*ForecastResponse, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", forecastURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "GoWeatherCLI/1.0 (github.com/yourusername/go-weather-cli)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: %s - %s", resp.Status, string(body))
	}

	var data ForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	return &data, nil
}

func filterAndDisplayForecastByDate(points *PointsResponse, forecast *ForecastResponse, date string) {
	targetDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		fmt.Printf("Error parsing date: %s. Please use format YYYY-MM-DD\n", err)
		return
	}

	fmt.Printf("\nWeather forecast for %s, %s on %s\n",
		points.Properties.RelativeLocation.Properties.City,
		points.Properties.RelativeLocation.Properties.State,
		targetDate.Format("Monday, January 2, 2006"))
	fmt.Printf("------------------------------\n")

	found := false
	for _, period := range forecast.Properties.Periods {
		periodStart, err := time.Parse(time.RFC3339, period.StartTime)
		if err != nil {
			continue
		}

		periodEnd, err := time.Parse(time.RFC3339, period.EndTime)
		if err != nil {
			continue
		}

		if periodStart.Format("2006-01-02") == targetDate.Format("2006-01-02") ||
			periodEnd.Format("2006-01-02") == targetDate.Format("2006-01-02") {

			icon := getWeatherIcon(period.ShortForecast)
			fmt.Printf("%s: %s %s\n", period.Name, icon, period.ShortForecast)
			fmt.Printf("Temperature: %d°%s\n", period.Temperature, period.TemperatureUnit)
			fmt.Printf("Wind: %s %s\n", period.WindSpeed, period.WindDirection)
			fmt.Printf("Period: %s to %s\n",
				periodStart.Format("3:04 PM"),
				periodEnd.Format("3:04 PM"))
			fmt.Printf("Detailed: %s\n", period.DetailedForecast)
			fmt.Printf("------------------------------\n")
			found = true
		}
	}

	if !found {
		fmt.Printf("No forecast data available for %s\n", targetDate.Format("Monday, January 2, 2006"))
		fmt.Printf("Forecast data is typically available for 7 days\n")
	}
}

func displayWeather(points *PointsResponse, forecast *ForecastResponse, detailed, hourly bool) {
	location := points.Properties.RelativeLocation.Properties
	fmt.Printf("\nWeather for %s, %s\n", location.City, location.State)
	fmt.Printf("------------------------------\n")
	fmt.Printf("Last updated: %s\n", forecast.Properties.Updated)
	fmt.Printf("------------------------------\n")

	if len(forecast.Properties.Periods) > 0 {
		current := forecast.Properties.Periods[0]

		icon := getWeatherIcon(current.ShortForecast)

		fmt.Printf("%s: %s %s\n", current.Name, icon, current.ShortForecast)
		fmt.Printf("Temperature: %d°%s\n", current.Temperature, current.TemperatureUnit)
		fmt.Printf("Wind: %s %s\n", current.WindSpeed, current.WindDirection)

		if detailed {
			fmt.Printf("\nDetailed forecast: %s\n", current.DetailedForecast)
		}

		periodLimit := 3
		if detailed {
			periodLimit = 10
		}
		if hourly {
			periodLimit = 24
		}

		if hourly && len(forecast.Properties.Periods) > 1 {
			fmt.Printf("\n------------------------------\n")
			fmt.Printf("Hourly Forecast:\n")
			fmt.Printf("------------------------------\n")

			for i := 1; i < len(forecast.Properties.Periods) && i < periodLimit; i++ {
				period := forecast.Properties.Periods[i]
				periodTime, _ := time.Parse(time.RFC3339, period.StartTime)

				icon := getWeatherIcon(period.ShortForecast)
				fmt.Printf("\n%s (%s): %s %s\n",
					period.Name,
					periodTime.Format("3:04 PM"),
					icon,
					period.ShortForecast)
				fmt.Printf("Temperature: %d°%s\n", period.Temperature, period.TemperatureUnit)
				fmt.Printf("Wind: %s %s\n", period.WindSpeed, period.WindDirection)
			}
		}

		if !hourly && len(forecast.Properties.Periods) > 1 {
			fmt.Printf("\n------------------------------\n")
			fmt.Printf("Extended Forecast:\n")
			fmt.Printf("------------------------------\n")

			for i := 1; i < len(forecast.Properties.Periods) && i < periodLimit; i++ {
				period := forecast.Properties.Periods[i]
				icon := getWeatherIcon(period.ShortForecast)
				fmt.Printf("\n%s: %s %s\n", period.Name, icon, period.ShortForecast)
				fmt.Printf("Temperature: %d°%s\n", period.Temperature, period.TemperatureUnit)
				fmt.Printf("Wind: %s %s\n", period.WindSpeed, period.WindDirection)

				if detailed {
					fmt.Printf("Detailed: %s\n", period.DetailedForecast)
				}
			}
		}
	}
}

func loadConfig() Config {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        fmt.Printf("Warning: Could not determine home directory: %s\n", err)
        return Config{}
    }

    configPath := filepath.Join(homeDir, CacheDir, ConfigFile)
    if _, err := os.Stat(configPath); os.IsNotExist(err) {
        return Config{}
    }

    data, err := os.ReadFile(configPath)  // Changed from ioutil.ReadFile
    if err != nil {
        fmt.Printf("Warning: Could not read config file: %s\n", err)
        return Config{}
    }

    var config Config
    if err := json.Unmarshal(data, &config); err != nil {
        fmt.Printf("Warning: Could not parse config file: %s\n", err)
        return Config{}
    }

    return config
}

func saveConfig(config Config) {
    homeDir, err := os.UserHomeDir()
    if err != nil {
        fmt.Printf("Error: Could not determine home directory: %s\n", err)
        return
    }

    configPath := filepath.Join(homeDir, CacheDir, ConfigFile)
    data, err := json.MarshalIndent(config, "", "  ")
    if err != nil {
        fmt.Printf("Error: Could not serialize config: %s\n", err)
        return
    }

    if err := os.WriteFile(configPath, data, 0644); err != nil {  // Changed from ioutil.WriteFile
        fmt.Printf("Error: Could not save config file: %s\n", err)
    }
}

func getFromCache(key string) (interface{}, bool) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}

	cachePath := filepath.Join(homeDir, CacheDir, WeatherCacheFile)
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		return nil, false
	}

	data, err := os.ReadFile(cachePath)  // Changed from ioutil.ReadFile
	if err != nil {
		return nil, false
	}

	var cache Cache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, false
	}

	entry, found := cache[key]
	if !found {
		return nil, false
	}

	if time.Now().After(entry.Expiry) {
		return nil, false
	}

	return entry.Data, true
}

func saveToCache(key string, data interface{}, duration time.Duration) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	cachePath := filepath.Join(homeDir, CacheDir, WeatherCacheFile)
	var cache Cache

	// Load existing cache if it exists
	if cacheData, err := os.ReadFile(cachePath); err == nil {
		json.Unmarshal(cacheData, &cache)
	}

	if cache == nil {
		cache = make(Cache)
	}

	// Create new cache entry
	cache[key] = CacheEntry{
		Timestamp: time.Now(),
		Data:      data,
		Expiry:    time.Now().Add(duration),
	}

	// Save updated cache
	if cacheData, err := json.MarshalIndent(cache, "", "  "); err == nil {
		os.WriteFile(cachePath, cacheData, 0644)
	}
}