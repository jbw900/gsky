package main

/* ows is a web server implementing the WMS, WCS and WPS protocols
   to serve geospatial data. This server is intended to be
   consumed directly by users and exposes a series of
   functionalities through the GetCapabilities.xml document.
   Configuration of the server is specified in the config.json
   file where features such as layers or color scales can be
   defined.
   This server depends on two other services to operate: the
   index server which registers the files involved in one operation
   and the warp server which performs the actual rendering of
   a tile. */
import (
//	"avs"
//"encoding/xml"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
//"strconv"
//"os/exec"
//"reflect"
"bufio"

	proc "github.com/nci/gsky/processor"
	"github.com/nci/gsky/utils"

	_ "net/http/pprof"

	geo "github.com/nci/geometry"
)

// Global variable to hold the values specified
// on the config.json document.
var configMap map[string]*utils.Config

var (
	port            = flag.Int("p", 8080, "Server listening port.")
	serverDataDir   = flag.String("data_dir", utils.DataDir, "Server data directory.")
	serverConfigDir = flag.String("conf_dir", utils.EtcDir, "Server config directory.")
	validateConfig  = flag.Bool("check_conf", false, "Validate server config files.")
	dumpConfig      = flag.Bool("dump_conf", false, "Dump server config files.")
	verbose         = flag.Bool("v", false, "Verbose mode for more server outputs.")
	thredds         = flag.Bool("t", false, "Save the *.nc files on THREDDS.")
	dap         	= flag.Bool("dap", true, "For DAP-GSKY Service.")
)

var reWMSMap map[string]*regexp.Regexp
var reWCSMap map[string]*regexp.Regexp
var reWPSMap map[string]*regexp.Regexp

var (
	Error *log.Logger
	Info  *log.Logger
)


// AVS: Debugging functions
// AVS
func P(text string) {
    fmt.Printf("%+v\n", text)
}
func Py(n byte) {
    fmt.Printf("%+v\n", n)
}
func Pf(n float64) {
    fmt.Printf("%+f\n", n)
}
func Pi(n int) {
    fmt.Printf("%+v\n", n)
}
func Pb(text bool) {
    fmt.Printf("%+v\n", text)
}
// Pretty print a map object
func Pm(v map[string][]string) {
	for k := range v {
		fmt.Printf("%+v: %+v\n", k, v[k])
	}
}

func Pu(item  string) {
	out, err := json.Marshal(item)
	if err != nil {
		panic (err)
	}
	P(string(out))
}

// init initialises the Error logger, checks
// required files are in place  and sets Config struct.
// This is the first function to be called in main.
func init() {
	rand.Seed(time.Now().UnixNano())

	Error = log.New(os.Stderr, "OWS: ", log.Ldate|log.Ltime|log.Lshortfile)
	Info = log.New(os.Stdout, "OWS: ", log.Ldate|log.Ltime|log.Lshortfile)

	flag.Parse()

	utils.DataDir = *serverDataDir
	utils.EtcDir = *serverConfigDir

	filePaths := []string{
		utils.DataDir + "/static/index.html",
		utils.DataDir + "/templates/WMS_GetCapabilities.tpl",
		utils.DataDir + "/templates/WMS_DescribeLayer.tpl",
		utils.DataDir + "/templates/WMS_ServiceException.tpl",
		utils.DataDir + "/templates/WPS_DescribeProcess.tpl",
		utils.DataDir + "/templates/WPS_Execute.tpl",
		utils.DataDir + "/templates/WPS_GetCapabilities.tpl",
		utils.DataDir + "/templates/WCS_GetCapabilities.tpl",
		utils.DataDir + "/templates/WCS_DescribeCoverage.tpl",
		utils.DataDir + "/templates/WMS_GetCapabilities_v1.1.1.tpl"}

	for _, filePath := range filePaths {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			panic(err)
		}
	}

	confMap, err := utils.LoadAllConfigFiles(utils.EtcDir, *verbose)

	if err != nil {
		Error.Printf("Error in loading config files: %v\n", err)
		panic(err)
	}

	if *validateConfig {
		os.Exit(0)
	}

	if *dumpConfig {
		configJson, err := utils.DumpConfig(confMap)
		if err != nil {
			Error.Printf("Error in dumping configs: %v\n", err)
		} else {
			log.Print(configJson)
		}
		os.Exit(0)
	}

	configMap = confMap
	utils.WatchConfig(Info, Error, &configMap, *verbose)

	reWMSMap = utils.CompileWMSRegexMap()
	reWCSMap = utils.CompileWCSRegexMap()
	reWPSMap = utils.CompileWPSRegexMap()

}
func list (reqURL string) { // AVS
    s := strings.Split(reqURL, "&")
    for i := range s {
    	st := s[i]
    	st = strings.Replace(st,"%3A", ":", -1)
    	st = strings.Replace(st,"%2F", "/", -1)
    	st = strings.Replace(st,"%2C", ",", -1)
//fmt.Println(st)	
    }

}
func serveWMS(ctx context.Context, params utils.WMSParams, conf *utils.Config, reqURL string, w http.ResponseWriter, r *http.Request) {
Info.Printf("reqURL: %v\n", reqURL)
	if params.Request == nil {
		http.Error(w, "Malformed WMS, a Request field needs to be specified", 400)
		return
	}
	switch *params.Request {
	case "GetCapabilities":
		if params.Version != nil && !utils.CheckWMSVersion(*params.Version) {
			http.Error(w, fmt.Sprintf("This server can only accept WMS requests compliant with version 1.1.1 and 1.3.0: %s", reqURL), 400)
			return
		}

		for iLayer := range conf.Layers {
			conf.GetLayerDates(iLayer, *verbose)
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
		GetCapabilities_template := "/templates/WMS_GetCapabilities.tpl";
		if (*params.Version == "1.1.1") {
			GetCapabilities_template = "/templates/WMS_GetCapabilities_v1.1.1.tpl";
		}
		err := utils.ExecuteWriteTemplateFile(w, conf,
			utils.DataDir+GetCapabilities_template)
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	case "GetFeatureInfo":
		x, y, err := utils.GetCoordinates(params)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS GetFeatureInfo request: %v", err), 400)
			return
		}

		var timeStr string
		if params.Time != nil {
			timeStr = fmt.Sprintf(`"time": "%s"`, (*params.Time).Format(utils.ISOFormat))
		}

		feat_info, err := proc.GetFeatureInfo(ctx, params, conf, *verbose)
		if err != nil {
			feat_info = fmt.Sprintf(`"error": "%v"`, err)
			Error.Printf("%v\n", err)
		}

		resp := fmt.Sprintf(`{"type":"FeatureCollection","features":[{"type":"Feature","properties":{"x":%f, "y":%f, %s, %s}}]}`, x, y, timeStr, feat_info)
		w.Write([]byte(resp))

	case "DescribeLayer":
		idx, err := utils.GetLayerIndex(params, conf)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS DescribeLayer request: %v", err), 400)
			return
		}

		err = utils.ExecuteWriteTemplateFile(w, conf.Layers[idx],
			utils.DataDir+"/templates/WMS_DescribeLayer.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}

	case "GetMap":
//		if *thredds {
//			proc.Init_thredds(w, r) // AVS: Create/use teh user-specific thredds subdir
//		}
		if params.Version == nil || !utils.CheckWMSVersion(*params.Version) {
			http.Error(w, fmt.Sprintf("This server can only accept WMS requests compliant with version 1.1.1 and 1.3.0: %s", reqURL), 400)
			return
		}

		idx, err := utils.GetLayerIndex(params, conf)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS GetMap request: %v", err), 400)
			return
		}
		if params.Time == nil {
			currentTime, err := utils.GetCurrentTimeStamp(conf.Layers[idx].Dates)
			if err != nil {
				http.Error(w, fmt.Sprintf("%v: %s", err, reqURL), 400)
				return
			}
			params.Time = currentTime
		}
		if params.CRS == nil {
			http.Error(w, fmt.Sprintf("Request %s should contain a valid ISO 'crs/srs' parameter.", reqURL), 400)
			return
		}
		if len(params.BBox) != 4 {
			http.Error(w, fmt.Sprintf("Request %s should contain a valid 'bbox' parameter.", reqURL), 400)
			return
		}
		if params.Height == nil || params.Width == nil {
			http.Error(w, fmt.Sprintf("Request %s should contain valid 'width' and 'height' parameters.", reqURL), 400)
			return
		}

		if strings.ToUpper(*params.CRS) == "EPSG:4326" && *params.Version == "1.3.0" {
			params.BBox = []float64{params.BBox[1], params.BBox[0], params.BBox[3], params.BBox[2]}
		}

		if strings.ToUpper(*params.CRS) == "CRS:84" && *params.Version == "1.3.0" {
			*params.CRS = "EPSG:4326"
		}

		var endTime *time.Time
		if conf.Layers[idx].Accum == true {
			step := time.Minute * time.Duration(60*24*conf.Layers[idx].StepDays+60*conf.Layers[idx].StepHours+conf.Layers[idx].StepMinutes)
			eT := params.Time.Add(step)
			endTime = &eT
		}
		if *params.Height > conf.Layers[idx].WmsMaxHeight || *params.Width > conf.Layers[idx].WmsMaxWidth {
			http.Error(w, fmt.Sprintf("Requested width/height is too large, max width:%d, height:%d", conf.Layers[idx].WmsMaxWidth, conf.Layers[idx].WmsMaxHeight), 400)
		}
//Info.Printf("params.Styles: %+v\n", params.Styles)
// AVS: If the call is from Google Earth, the 'Styles=default' in the URL crashes the function. Change it to ""
		if (*params.Version == "1.1.1") {
			params.Styles[0] = ""
		}
		styleIdx, err := utils.GetLayerStyleIndex(params, conf, idx)
//Info.Printf("params: %+v\n", *params.Time)
//Info.Printf("err: %+v\n", err)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS GetMap request: %v", err), 400)
			return
		}
		styleLayer := &conf.Layers[idx]
		if styleIdx >= 0 {
			styleLayer = &conf.Layers[idx].Styles[styleIdx]
		}
		geoReq := &proc.GeoTileRequest{ConfigPayLoad: proc.ConfigPayLoad{NameSpaces: styleLayer.RGBExpressions.VarList,
			BandExpr: styleLayer.RGBExpressions,
			Mask:     styleLayer.Mask,
			Palette:  styleLayer.Palette,
			ScaleParams: proc.ScaleParams{Offset: styleLayer.OffsetValue,
				Scale: styleLayer.ScaleValue,
				Clip:  styleLayer.ClipValue,
			},
			ZoomLimit:       conf.Layers[idx].ZoomLimit,
			PolygonSegments: conf.Layers[idx].WmsPolygonSegments,
			GrpcConcLimit:   conf.Layers[idx].GrpcWmsConcPerNode,
			QueryLimit:      -1,
		},
			Collection: styleLayer.DataSource,
			CRS:        *params.CRS,
			BBox:       params.BBox,
			Height:     *params.Height,
			Width:      *params.Width,
			StartTime:  params.Time,
			EndTime:    endTime,
		}
		ctx, ctxCancel := context.WithCancel(ctx)
//fmt.Printf("ctx: %+v\n", ctx)

		defer ctxCancel()
		errChan := make(chan error, 100)

		xRes := (params.BBox[2] - params.BBox[0]) / float64(*params.Width)
		yRes := (params.BBox[3] - params.BBox[1]) / float64(*params.Height)
		reqRes := xRes
		if yRes > reqRes {
			reqRes = yRes
		}
		// AVS: With v 1.1.1 and others that send EPSG:4326 the value is less than 1
		if (*params.Version == "1.1.1") {
			reqRes = reqRes * 100000
		}
		if conf.Layers[idx].ZoomLimit != 0.0 && reqRes > conf.Layers[idx].ZoomLimit {
			indexer := proc.NewTileIndexer(ctx, conf.ServiceConfig.MASAddress, errChan)
			go func() {
				geoReq.Mask = nil
				geoReq.QueryLimit = 1
				indexer.In <- geoReq
				close(indexer.In)
			}()

			go indexer.Run(*verbose)

			hasData := false
			for geo := range indexer.Out {
				select {
				case <-errChan:
					break
				case <-ctx.Done():
					break
				default:
					if geo.NameSpace != "EmptyTile" {
						hasData = true
						break
					}
				}

				if hasData {
					break
				}
			}

			if hasData {
				out, err := utils.GetEmptyTile(utils.DataDir+"/zoom.png", *params.Height, *params.Width)
				if err != nil {
					Info.Printf("Error in the utils.GetEmptyTile(zoom.png): %v\n", err)
					http.Error(w, err.Error(), 500)
					return
				}
				w.Write(out)
			} else {
				out, err := utils.GetEmptyTile("", *params.Height, *params.Width)
				if err != nil {
					Info.Printf("Error in the utils.GetEmptyTile(): %v\n", err)
					http.Error(w, err.Error(), 500)
				} else {
					w.Write(out)
				}
			}

			return
		}
		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Duration(conf.Layers[idx].WmsTimeout)*time.Second)
		defer timeoutCancel()

		tp := proc.InitTilePipeline(ctx, conf.ServiceConfig.MASAddress, conf.ServiceConfig.WorkerNodes, conf.Layers[idx].MaxGrpcRecvMsgSize, conf.Layers[idx].WmsPolygonShardConcLimit, conf.ServiceConfig.MaxGrpcBufferSize, errChan)
		select {
		case res := <-tp.Process(geoReq, *verbose):
			scaleParams := utils.ScaleParams{Offset: geoReq.ScaleParams.Offset,
				Scale: geoReq.ScaleParams.Scale,
				Clip:  geoReq.ScaleParams.Clip,
			}
			norm, err := utils.Scale(res, scaleParams)
			if err != nil {
				Info.Printf("Error in the utils.Scale: %v\n", err)
				http.Error(w, err.Error(), 500)
				return
			}
//fmt.Printf("norm: %+v\n", norm[0])

			if len(norm) == 0 || norm[0].Width == 0 || norm[0].Height == 0 {
				out, err := utils.GetEmptyTile(conf.Layers[idx].NoDataLegendPath, *params.Height, *params.Width)
				if err != nil {
					Info.Printf("Error in the utils.GetEmptyTile(): %v\n", err)
					http.Error(w, err.Error(), 500)
				} else {
					w.Write(out)
				}
				return
			}
			out, err := utils.EncodePNG(norm, styleLayer.Palette)
//fmt.Printf("out: %+v\n", out)
			if err != nil {
				Info.Printf("Error in the utils.EncodePNG: %v\n", err) 
				http.Error(w, err.Error(), 500)
				return
			}
			w.Write(out)
		case err := <-errChan:
			Info.Printf("Error in the pipeline: %v\n", err)
			http.Error(w, err.Error(), 500)
		case <-ctx.Done():
			Error.Printf("Context cancelled with message: %v\n", ctx.Err())
			http.Error(w, ctx.Err().Error(), 500)
		case <-timeoutCtx.Done():
			Error.Printf("WMS pipeline timed out, threshold:%v seconds", conf.Layers[idx].WmsTimeout)
			http.Error(w, "WMS request timed out", 500)
		}
// AVS: Call WCS here so that the displayed map in the canvas extent is saved as a NetCDF file
//query := utils.NormaliseKeys(r.URL.Query())
//query["service"][0] = "WCS"
//query["request"][0] = "GetCoverage"
//query["srs"][0] = "EPSG:3857"
//Info.Printf("query: %+v\n", query)
//ctx = r.Context()
//Info.Printf("params=%+v", params)
//Time := *params.Time
//*params.Time = Time
//Info.Printf("params.Request: %+v\n", *params.Request)
//Info.Printf("w: %+v\n", w)
//Info.Printf("URL: %+v\n", r.URL.String())
//params, err := utils.WCSParamsChecker(query, reWCSMap) // AVS: Time added to prevent a NIL value 
//serveWCS(ctx, params, conf, r.URL.String(), w, query)
		return

	case "GetLegendGraphic":
		idx, err := utils.GetLayerIndex(params, conf)
		if err != nil {
			Error.Printf("%s\n", err)
			if len(params.Layers) > 0 {
				utils.ExecuteWriteTemplateFile(w, params.Layers[0],
					utils.DataDir+"/templates/WMS_ServiceException.tpl")
			} else {
				http.Error(w, err.Error(), 400)
			}
			return
		}
		styleIdx, err := utils.GetLayerStyleIndex(params, conf, idx)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS GetMap request: %v", err), 400)
			return
		}

		styleLayer := &conf.Layers[idx]
		if styleIdx >= 0 {
			styleLayer = &conf.Layers[idx].Styles[styleIdx]
		}

		b, err := ioutil.ReadFile(styleLayer.LegendPath)
		if err != nil {
			Error.Printf("Error reading legend image: %v, %v\n", styleLayer.LegendPath, err)
			http.Error(w, "Legend graphics not found", 500)
			return
		}
		w.Write(b)

	default:
		http.Error(w, fmt.Sprintf("%s not recognised.", *params.Request), 400)
	}
}
func Save_aggregate_netcdf (masterTempFile string) {
/*
AVS:
	- This func copies the NetCDF file master (e.g. /tmp/raster_123332973) to /tmp/aggregate_netcdf.nc
	- From there, a cron job that runs every minute will copy it to...
		- /home/900/avs900/OpenDAP/aggregate_netcdf.nc and
		- /usr/local/tds/apache-tomcat-8.5.35/content/thredds/public/gsky/gsky_test.nc # For THREDDS
It has to be done this way, as the user, 'fr5_gsky', GSKY server runs under cannot write
or copy files to any user directory.

*/
	source, err := os.Open(masterTempFile)
	if err != nil {
		errMsg := fmt.Sprintf("Reading failed: %v, %v", err, source)
		Info.Printf(errMsg)
	}
	defer source.Close()
	
	dst := "/tmp/aggregate_netcdf.nc"
	destination, err := os.Create(dst)
	if err != nil {
		errMsg := fmt.Sprintf("Creation failed: %v, %v", err, destination)
		Info.Printf(errMsg)
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	if err != nil {
		errMsg := fmt.Sprintf("Writing failed: %v, %v, %v", err, destination, nBytes)
		Info.Printf(errMsg)
	}
}
func serveWCS(ctx context.Context, params utils.WCSParams, conf *utils.Config, reqURL string, w http.ResponseWriter, query map[string][]string) {
//Info.Printf("params=%+v", params)
	if params.Request == nil {
		http.Error(w, "Malformed WCS, a Request field needs to be specified", 400)
	}

	switch *params.Request {
	case "GetCapabilities":
		if params.Version != nil && !utils.CheckWCSVersion(*params.Version) {
			http.Error(w, fmt.Sprintf("This server can only accept WCS requests compliant with version 1.0.0: %s", reqURL), 400)
			return
		}

		newConf := *conf
		newConf.Layers = make([]utils.Layer, len(newConf.Layers))
		for i, layer := range conf.Layers {
			conf.GetLayerDates(i, *verbose)
			newConf.Layers[i] = layer
			newConf.Layers[i].Dates = []string{newConf.Layers[i].Dates[0], newConf.Layers[i].Dates[len(newConf.Layers[i].Dates)-1]}
		}

		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
		err := utils.ExecuteWriteTemplateFile(w, &newConf, utils.DataDir+"/templates/WCS_GetCapabilities.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}

	case "DescribeCoverage":
		idx, err := utils.GetCoverageIndex(params, conf)
		if err != nil {
			Info.Printf("Error in the pipeline: %v\n", err)
			http.Error(w, fmt.Sprintf("Malformed WMS DescribeCoverage request: %v", err), 400)
			return
		}

		err = utils.ExecuteWriteTemplateFile(w, conf.Layers[idx], utils.DataDir+"/templates/WCS_DescribeCoverage.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}

	case "GetCoverage":
		if params.Version == nil || !utils.CheckWCSVersion(*params.Version) {
			http.Error(w, fmt.Sprintf("This server can only accept WCS requests compliant with version 1.0.0: %s", reqURL), 400)
				return
		}

		idx, err := utils.GetCoverageIndex(params, conf)
		if err != nil {
			http.Error(w, fmt.Sprintf("%v: %s", err, reqURL), 400)
			return
		}

		if params.Time == nil {
			currentTime, err := utils.GetCurrentTimeStamp(conf.Layers[idx].Dates)
			if err != nil {
				http.Error(w, fmt.Sprintf("%v: %s", err, reqURL), 400)
				return
			}
			params.Time = currentTime
		}
		if params.CRS == nil {
			http.Error(w, fmt.Sprintf("Request %s should contain a valid ISO 'crs/srs' parameter.", reqURL), 400)
			return
		}
		if len(params.BBox) != 4 {
			http.Error(w, fmt.Sprintf("Request %s should contain a valid 'bbox' parameter.", reqURL), 400)
			return
		}
		if params.Height == nil || params.Width == nil {
			http.Error(w, fmt.Sprintf("Request %s should contain valid 'width' and 'height' parameters.", reqURL), 400)
			return
		}
		if params.Format == nil {
			http.Error(w, fmt.Sprintf("Unsupported encoding format"), 400)
			return
		}
		var endTime *time.Time
		if conf.Layers[idx].Accum == true {
			step := time.Minute * time.Duration(60*24*conf.Layers[idx].StepDays+60*conf.Layers[idx].StepHours+conf.Layers[idx].StepMinutes)
			eT := params.Time.Add(step)
			endTime = &eT
		}

		styleIdx, err := utils.GetCoverageStyleIndex(params, conf, idx)
		if err != nil {
			Error.Printf("%s\n", err)
			http.Error(w, fmt.Sprintf("Malformed WCS GetCoverage request: %v", err), 400)
			return
		} else if styleIdx < 0 {
//			Error.Printf("WCS style not specified")
//			http.Error(w, "WCS style not specified", 400)
//			return
			styleCount := len(conf.Layers[idx].Styles)
			if styleCount > 1 {
				Error.Printf("WCS style not specified")
				http.Error(w, "WCS style not specified", 400)
				return
			} else if styleCount == 1 {
				styleIdx = 0
			}
		}

		styleLayer := &conf.Layers[idx]
		if styleIdx >= 0 {
			styleLayer = &conf.Layers[idx].Styles[styleIdx]
		}

		maxXTileSize := conf.Layers[idx].WcsMaxTileWidth
		maxYTileSize := conf.Layers[idx].WcsMaxTileHeight
		checkpointThreshold := 300
		minTilesPerWorker := 5

		var wcsWorkerNodes []string
		workerTileRequests := [][]*proc.GeoTileRequest{}

		_, isWorker := query["wbbox"]

		getGeoTileRequest := func(width int, height int, bbox []float64, offX int, offY int) *proc.GeoTileRequest {
			geoReq := &proc.GeoTileRequest{ConfigPayLoad: proc.ConfigPayLoad{NameSpaces: styleLayer.RGBExpressions.VarList,
				BandExpr: styleLayer.RGBExpressions,
				Mask:     styleLayer.Mask,
				Palette:  styleLayer.Palette,
				ScaleParams: proc.ScaleParams{Offset: styleLayer.OffsetValue,
					Scale: styleLayer.ScaleValue,
					Clip:  styleLayer.ClipValue,
				},
				ZoomLimit:       0.0,
				PolygonSegments: conf.Layers[idx].WcsPolygonSegments,
				GrpcConcLimit:   conf.Layers[idx].GrpcWcsConcPerNode,
				QueryLimit:      -1,
			},
				Collection: styleLayer.DataSource,
				CRS:        *params.CRS,
				BBox:       bbox,
				Height:     height,
				Width:      width,
				StartTime:  params.Time,
				EndTime:    endTime,
				OffX:       offX,
				OffY:       offY,
			}
			return geoReq
		}

		ctx, ctxCancel := context.WithCancel(ctx)
		defer ctxCancel()
		errChan := make(chan error, 100)

		epsg, err := utils.ExtractEPSGCode(*params.CRS)
		if err != nil {
			http.Error(w, fmt.Sprintf("Invalid CRS code %s", *params.CRS), 400)
			return
		}

		if *params.Width <= 0 || *params.Height <= 0 {
			if isWorker {
				msg := "WCS: worker width or height negative"
				Info.Printf(msg)
				http.Error(w, msg, 500)
				return
			}

			geoReq := getGeoTileRequest(0, 0, params.BBox, 0, 0)
			maxWidth, maxHeight, err := proc.ComputeReprojectionExtent(ctx, geoReq, conf.ServiceConfig.MASAddress, conf.ServiceConfig.WorkerNodes, epsg, params.BBox, *verbose)
			if *verbose {
				Info.Printf("WCS: Output image size: width=%v, height=%v", maxWidth, maxHeight)
			}
			if maxWidth > 0 && maxHeight > 0 {
				*params.Width = maxWidth
				*params.Height = maxHeight

				rex := regexp.MustCompile(`(?i)&width\s*=\s*[-+]?[0-9]+`)
				reqURL = rex.ReplaceAllString(reqURL, ``)

				rex = regexp.MustCompile(`(?i)&height\s*=\s*[-+]?[0-9]+`)
				reqURL = rex.ReplaceAllString(reqURL, ``)

				reqURL += fmt.Sprintf("&width=%d&height=%d", maxWidth, maxHeight)
			} else {
				errMsg := "WCS: failed to compute output extent"
				Info.Printf(errMsg, err)
				http.Error(w, errMsg, 500)
				return
			}

		}

		if *params.Height > conf.Layers[idx].WcsMaxHeight || *params.Width > conf.Layers[idx].WcsMaxWidth {
			http.Error(w, fmt.Sprintf("Requested width/height is too large, max width:%d, height:%d", conf.Layers[idx].WcsMaxWidth, conf.Layers[idx].WcsMaxHeight), 400)
			return
		}

		if !isWorker {
			if *params.Width > maxXTileSize || *params.Height > maxYTileSize {
				tmpTileRequests := []*proc.GeoTileRequest{}
				xRes := (params.BBox[2] - params.BBox[0]) / float64(*params.Width)
				yRes := (params.BBox[3] - params.BBox[1]) / float64(*params.Height)

				for y := 0; y < *params.Height; y += maxYTileSize {
					for x := 0; x < *params.Width; x += maxXTileSize {
						yMin := params.BBox[1] + float64(y)*yRes
						yMax := math.Min(params.BBox[1]+float64(y+maxYTileSize)*yRes, params.BBox[3])
						xMin := params.BBox[0] + float64(x)*xRes
						xMax := math.Min(params.BBox[0]+float64(x+maxXTileSize)*xRes, params.BBox[2])

						tileXSize := int(.5 + (xMax-xMin)/xRes)
						tileYSize := int(.5 + (yMax-yMin)/yRes)

						geoReq := getGeoTileRequest(tileXSize, tileYSize, []float64{xMin, yMin, xMax, yMax}, x, *params.Height-y-tileYSize)
						tmpTileRequests = append(tmpTileRequests, geoReq)
					}
				}

				for iw, worker := range conf.ServiceConfig.OWSClusterNodes {
					parsedURL, err := url.Parse(worker)
					if err != nil {
						if *verbose {
							Info.Printf("WCS: invalid worker hostname %v, (%v of %v)\n", worker, iw, len(conf.ServiceConfig.OWSClusterNodes))
						}
						continue
					}

					if parsedURL.Host == conf.ServiceConfig.OWSHostname {
						if *verbose {
							Info.Printf("WCS: skipping worker whose hostname == OWSHostName %v, (%v of %v)\n", worker, iw, len(conf.ServiceConfig.OWSClusterNodes))
						}
						continue
					}
					wcsWorkerNodes = append(wcsWorkerNodes, worker)
				}

				nWorkers := len(wcsWorkerNodes) + 1
				tilesPerWorker := int(math.Round(float64(len(tmpTileRequests)) / float64(nWorkers)))
				if tilesPerWorker < minTilesPerWorker {
					tilesPerWorker = minTilesPerWorker
				}

				isLastWorker := false
				for i := 0; i < nWorkers; i++ {
					iBgn := i * tilesPerWorker
					iEnd := iBgn + tilesPerWorker
					if iEnd > len(tmpTileRequests) {
						iEnd = len(tmpTileRequests)
						isLastWorker = true
					}

					workerTileRequests = append(workerTileRequests, tmpTileRequests[iBgn:iEnd])
					if isLastWorker {
						break
					}
				}

			} else {
				geoReq := getGeoTileRequest(*params.Width, *params.Height, params.BBox, 0, 0)
				workerTileRequests = append(workerTileRequests, []*proc.GeoTileRequest{geoReq})
			}
		} else {
			for _, qParams := range []string{"wwidth", "wheight", "woffx", "woffy"} {
				if len(query[qParams]) != len(query["wbbox"]) {
					http.Error(w, fmt.Sprintf("worker parameter %v has different length from wbbox: %v", qParams, reqURL), 400)
					return
				}
			}

			workerBbox := query["wbbox"]
			workerWidth := query["wwidth"]
			workerHeight := query["wheight"]
			workerOffX := query["woffx"]
			workerOffY := query["woffy"]

			wParams := make(map[string][]string)
			wParams["bbox"] = []string{""}
			wParams["width"] = []string{""}
			wParams["height"] = []string{""}
			wParams["x"] = []string{""}
			wParams["y"] = []string{""}

			tmpTileRequests := []*proc.GeoTileRequest{}
			for iw, bbox := range workerBbox {
				wParams["bbox"][0] = bbox
				wParams["width"][0] = workerWidth[iw]
				wParams["height"][0] = workerHeight[iw]
				wParams["x"][0] = workerOffX[iw]
				wParams["y"][0] = workerOffY[iw]

				workerParams, err := utils.WMSParamsChecker(wParams, reWMSMap)
				if err != nil {
					http.Error(w, fmt.Sprintf("worker parameter error: %v", err), 400)
					return
				}

				geoReq := getGeoTileRequest(*workerParams.Width, *workerParams.Height, workerParams.BBox, *workerParams.X, *workerParams.Y)
				tmpTileRequests = append(tmpTileRequests, geoReq)
			}

			workerTileRequests = append(workerTileRequests, tmpTileRequests)
		}

		hDstDS := utils.GetDummyGDALDatasetH()
		var masterTempFile string

		tempFileGeoReq := make(map[string][]*proc.GeoTileRequest)

		workerErrChan := make(chan error, 100)
		workerDoneChan := make(chan string, len(workerTileRequests)-1)

		if !isWorker && len(workerTileRequests) > 1 {
			for iw := 1; iw < len(workerTileRequests); iw++ {
				workerHostName := wcsWorkerNodes[iw-1]
				queryURL := workerHostName + reqURL
				for _, geoReq := range workerTileRequests[iw] {
					paramStr := fmt.Sprintf("&wbbox=%f,%f,%f,%f&wwidth=%d&wheight=%d&woffx=%d&woffy=%d",
						geoReq.BBox[0], geoReq.BBox[1], geoReq.BBox[2], geoReq.BBox[3], geoReq.Width, geoReq.Height, geoReq.OffX, geoReq.OffY)

					queryURL += paramStr
				}

				if *verbose {
					Info.Printf("WCS worker (%v of %v): %v\n", iw, len(workerTileRequests)-1, queryURL)
				}

				trans := &http.Transport{}
				req, err := http.NewRequest("GET", queryURL, nil)
				if err != nil {
					errMsg := fmt.Sprintf("WCS: worker NewRequest error: %v", err)
					Info.Printf(errMsg)
					http.Error(w, errMsg, 500)
					return
				}
				defer trans.CancelRequest(req)

				tempFileHandle, err := ioutil.TempFile(conf.ServiceConfig.TempDir, "worker_raster_")
				if err != nil {
					errMsg := fmt.Sprintf("WCS: failed to create raster temp file for WCS worker: %v", err)
					Info.Printf(errMsg)
					http.Error(w, errMsg, 500)
					return
				}
				tempFileHandle.Close()
				defer os.Remove(tempFileHandle.Name())
				tempFileGeoReq[tempFileHandle.Name()] = workerTileRequests[iw]

				go func(req *http.Request, transport *http.Transport, tempFileName string) {
					client := &http.Client{Transport: transport}

					resp, err := client.Do(req)
					if err != nil {
						workerErrChan <- fmt.Errorf("WCS: worker error: %v", err)
						return
					}
					defer resp.Body.Close()

					tempFileHandle, err := os.Create(tempFileName)
					if err != nil {
						workerErrChan <- fmt.Errorf("failed to open raster temp file for WCS worker: %v\n", err)
						return
					}
					defer tempFileHandle.Close()

					_, err = io.Copy(tempFileHandle, resp.Body)
					if err != nil {
						tempFileHandle.Close()
						workerErrChan <- fmt.Errorf("WCS: worker error in io.Copy(): %v", err)
						return
					}

					workerDoneChan <- tempFileName
				}(req, trans, tempFileHandle.Name())
			}
		}

		geot := utils.BBox2Geot(*params.Width, *params.Height, params.BBox)

		driverFormat := *params.Format
		if isWorker {
			driverFormat = "geotiff" // or "NetCDF"
		}

		timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Duration(conf.Layers[idx].WcsTimeout)*time.Second)
		defer timeoutCancel()

		isInit := false

		tp := proc.InitTilePipeline(ctx, conf.ServiceConfig.MASAddress, conf.ServiceConfig.WorkerNodes, conf.Layers[idx].MaxGrpcRecvMsgSize, conf.Layers[idx].WcsPolygonShardConcLimit, conf.ServiceConfig.MaxGrpcBufferSize, errChan)
		for ir, geoReq := range workerTileRequests[0] {
			if *verbose {
				Info.Printf("WCS: processing tile (%d of %d): xOff:%v, yOff:%v, width:%v, height:%v", ir+1, len(workerTileRequests[0]), geoReq.OffX, geoReq.OffY, geoReq.Width, geoReq.Height)
			}

			select {
			case res := <-tp.Process(geoReq, *verbose):
				if !isInit {
					hDstDS, masterTempFile, err = utils.EncodeGdalOpen(conf.ServiceConfig.TempDir, 1024, 256, driverFormat, geot, epsg, res, *params.Width, *params.Height, len(styleLayer.RGBProducts))
					if err != nil {
						os.Remove(masterTempFile)
						errMsg := fmt.Sprintf("EncodeGdalOpen() failed: %v", err)
						Info.Printf(errMsg)
						http.Error(w, errMsg, 500)
						return
					}
					defer utils.EncodeGdalClose(&hDstDS)
					defer os.Remove(masterTempFile)

					isInit = true
				}

				err := utils.EncodeGdal(hDstDS, res, geoReq.OffX, geoReq.OffY)
				if err != nil {
					Info.Printf("Error in the utils.EncodeGdal: %v\n", err)
					http.Error(w, err.Error(), 500)
					return
				}

			case err := <-errChan:
				Info.Printf("WCS: error in the pipeline: %v\n", err)
				http.Error(w, err.Error(), 500)
				return
			case err := <-workerErrChan:
				Info.Printf("WCS worker error: %v\n", err)
				http.Error(w, err.Error(), 500)
				return
			case <-ctx.Done():
				Error.Printf("Context cancelled with message: %v\n", ctx.Err())
				http.Error(w, ctx.Err().Error(), 500)
				return
			case <-timeoutCtx.Done():
				Error.Printf("WCS pipeline timed out, threshold:%v seconds", conf.Layers[idx].WcsTimeout)
				http.Error(w, "WCS pipeline timed out", 500)
				return
			}

			if (ir+1)%checkpointThreshold == 0 {
				utils.EncodeGdalFlush(hDstDS)
				runtime.GC()
			}
		}

		if !isWorker && len(workerTileRequests) > 1 {
			nWorkerDone := 0
			allWorkerDone := false
			for {
				select {
				case workerTempFileName := <-workerDoneChan:
					offX := make([]int, len(tempFileGeoReq[workerTempFileName]))
					offY := make([]int, len(offX))
					width := make([]int, len(offX))
					height := make([]int, len(offX))

					for ig, geoReq := range tempFileGeoReq[workerTempFileName] {
						offX[ig] = geoReq.OffX
						offY[ig] = geoReq.OffY
						width[ig] = geoReq.Width
						height[ig] = geoReq.Height
					}

					var t0 time.Time
					if *verbose {
						t0 = time.Now()
					}
					err := utils.EncodeGdalMerge(ctx, hDstDS, "geotiff", workerTempFileName, width, height, offX, offY)
					if err != nil {
						Info.Printf("%v\n", err)
						http.Error(w, err.Error(), 500)
						return
					}
					os.Remove(workerTempFileName)
					nWorkerDone++

					if *verbose {
						t1 := time.Since(t0)
						Info.Printf("WCS: merge %v to %v done (%v of %v), time: %v", workerTempFileName, masterTempFile, nWorkerDone, len(workerTileRequests)-1, t1)
					}

					if nWorkerDone == len(workerTileRequests)-1 {
						allWorkerDone = true
					}
				case err := <-workerErrChan:
					Info.Printf("%v\n", err)
					http.Error(w, err.Error(), 500)
					return
				case <-ctx.Done():
					Error.Printf("Context cancelled with message: %v\n", ctx.Err())
					http.Error(w, ctx.Err().Error(), 500)
					return
				}

				if allWorkerDone {
					break
				}
			}
		}

		utils.EncodeGdalClose(&hDstDS)
		hDstDS = nil

		fileExt := "wcs"
		contentType := "application/wcs"
		switch strings.ToLower(*params.Format) {
		case "geotiff":
			fileExt = "tiff"
			contentType = "application/geotiff"
		case "netcdf":
			fileExt = "nc"
			contentType = "application/netcdf"
		}
		ISOFormat := "2006-01-02T15:04:05.000Z"
		fileNameDateTime := params.Time.Format(ISOFormat)

		var re = regexp.MustCompile(`[^a-zA-Z0-9\-_\s]`)
		fileNameCoverages := re.ReplaceAllString(params.Coverages[0], `-`)

		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.%s.%s", fileNameCoverages, fileNameDateTime, fileExt))
		w.Header().Set("Content-Type", contentType)

		fileHandle, err := os.Open(masterTempFile)
		if err != nil {
			errMsg := fmt.Sprintf("Error opening raster file: %v", err)
			Info.Printf(errMsg)
			http.Error(w, errMsg, 500)
		}
		defer fileHandle.Close()

		fileInfo, err := fileHandle.Stat()
		if err != nil {
			errMsg := fmt.Sprintf("file stat() failed: %v", err)
			Info.Printf(errMsg)
			http.Error(w, errMsg, 500)
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

		Save_aggregate_netcdf (masterTempFile) // AVS

		bytesSent, err := io.Copy(w, fileHandle)
		if err != nil {
			errMsg := fmt.Sprintf("SendFile failed: %v", err)
			Info.Printf(errMsg)
			http.Error(w, errMsg, 500)
		}

		if *verbose {
			Info.Printf("WCS: file_size:%v, bytes_sent:%v\n", fileInfo.Size(), bytesSent)
		}
		

		return

	default:
		http.Error(w, fmt.Sprintf("%s not recognised.", *params.Request), 400)
	}
}

// AVS --------------------------------------------
func check(e error) {
    if e != nil {
        panic(e)
    }
}

func avs_write() {

    // To start, here's how to dump a string (or just
    // bytes) into a file.
    d1 := []byte("hello\ngo\n")
    err := ioutil.WriteFile("/tmp/dat1", d1, 0644)
    check(err)

    // For more granular writes, open a file for writing.
    f, err := os.Create("/tmp/dat2")
    check(err)

    // It's idiomatic to defer a `Close` immediately
    // after opening a file.
    defer f.Close()

    // You can `Write` byte slices as you'd expect.
    d2 := []byte{115, 111, 109, 101, 10}
    n2, err := f.Write(d2)
    check(err)
    fmt.Printf("wrote %d bytes\n", n2)

    // A `WriteString` is also available.
    n3, err := f.WriteString("writes\n")
    fmt.Printf("wrote %d bytes\n", n3)

    // Issue a `Sync` to flush writes to stable storage.
    f.Sync()

    // `bufio` provides buffered writers in addition
    // to the buffered readers we saw earlier.
    w := bufio.NewWriter(f)
    n4, err := w.WriteString("buffered\n")
    fmt.Printf("wrote %d bytes\n", n4)

    // Use `Flush` to ensure all buffered operations have
    // been applied to the underlying writer.
    w.Flush()

}
// AVS --------------------------------------------


func serveWPS(ctx context.Context, params utils.WPSParams, conf *utils.Config, reqURL string, w http.ResponseWriter) {

	if params.Request == nil {
		http.Error(w, "Malformed WPS, a Request field needs to be specified", 400)
		return
	}

	switch *params.Request {
	case "GetCapabilities":
		err := utils.ExecuteWriteTemplateFile(w, conf,
			utils.DataDir+"/templates/WPS_GetCapabilities.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	case "DescribeProcess":
		idx, err := utils.GetProcessIndex(params, conf)
		if err != nil {
			Error.Printf("Requested process not found: %v, %v\n", err, reqURL)
			http.Error(w, fmt.Sprintf("%v: %s", err, reqURL), 400)
			return
		}
		process := conf.Processes[idx]
		err = utils.ExecuteWriteTemplateFile(w, process,
			utils.DataDir+"/templates/WPS_DescribeProcess.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}
	case "Execute":
		idx, err := utils.GetProcessIndex(params, conf)
		if err != nil {
			Error.Printf("Requested process not found: %v, %v\n", err, reqURL)
			http.Error(w, fmt.Sprintf("%v: %s", err, reqURL), 400)
			return
		}
		process := conf.Processes[idx]
		if len(process.DataSources) == 0 {
			Error.Printf("No data source specified")
			http.Error(w, "No data source specified", 500)
			return
		}

		if len(params.FeatCol.Features) == 0 {
			Info.Printf("The request does not contain the 'feature' property.\n")
			http.Error(w, "The request does not contain the 'feature' property", 400)
			return
		}

		var feat []byte
		geom := params.FeatCol.Features[0].Geometry
		switch geom := geom.(type) {

		case *geo.Point:
			feat, _ = json.Marshal(&geo.Feature{Type: "Feature", Geometry: geom})

		case *geo.Polygon, *geo.MultiPolygon:
			area := utils.GetArea(geom)
			log.Println("Requested polygon has an area of", area)
			if area == 0.0 || area > process.MaxArea {
				Info.Printf("The requested area %.02f, is too large.\n", area)
				http.Error(w, "The requested area is too large. Please try with a smaller one.", 400)
				return
			}
			feat, _ = json.Marshal(&geo.Feature{Type: "Feature", Geometry: geom})

		default:
			http.Error(w, "Geometry not supported. Only Features containing Polygon or MultiPolygon are available..", 400)
			return
		}

		var result string
		ctx, ctxCancel := context.WithCancel(ctx)
		defer ctxCancel()
		errChan := make(chan error, 100)
		suffix := fmt.Sprintf("_%04d", rand.Intn(1000))

		for ids, dataSource := range process.DataSources {
			log.Printf("WPS: Processing '%v' (%d of %d)", dataSource.DataSource, ids+1, len(process.DataSources))

			startDateTime := time.Time{}
			stStartInput, errStartInput := time.Parse(utils.ISOFormat, *params.StartDateTime)
			if errStartInput != nil {
				if len(*params.StartDateTime) > 0 {
					log.Printf("WPS: invalid input start date '%v' with error '%v'", *params.StartDateTime, errStartInput)
				}
				startDateTimeStr := strings.TrimSpace(dataSource.StartISODate)
				if len(startDateTimeStr) > 0 {
					st, errStart := time.Parse(utils.ISOFormat, startDateTimeStr)
					if errStart != nil {
						log.Printf("WPS: Failed to parse start date '%v' into ISO format with error: %v, defaulting to no start date", startDateTimeStr, errStart)
					} else {
						startDateTime = st
					}
				}
			} else {
				startDateTime = stStartInput
			}

			endDateTime := time.Now().UTC()
			stEndInput, errEndInput := time.Parse(utils.ISOFormat, *params.EndDateTime)
			if errEndInput != nil {
				if len(*params.EndDateTime) > 0 {
					log.Printf("WPS: invalid input end date '%v' with error '%v'", *params.EndDateTime, errEndInput)
				}
				endDateTimeStr := strings.TrimSpace(dataSource.EndISODate)
				if len(endDateTimeStr) > 0 && strings.ToLower(endDateTimeStr) != "now" {
					dt, errEnd := time.Parse(utils.ISOFormat, endDateTimeStr)
					if errEnd != nil {
						log.Printf("WPS: Failed to parse end date '%s' into ISO format with error: %v, defaulting to now()", endDateTimeStr, errEnd)
					} else {
						endDateTime = dt
					}
				}
			} else {
				if !time.Time.IsZero(stEndInput) {
					endDateTime = stEndInput
				}
			}

			geoReq := proc.GeoDrillRequest{Geometry: string(feat),
				CRS:        "EPSG:4326",
				Collection: dataSource.DataSource,
				NameSpaces: dataSource.RGBExpressions.VarList,
				BandExpr:   dataSource.RGBExpressions,
				StartTime:  startDateTime,
				EndTime:    endDateTime,
			}

			dp := proc.InitDrillPipeline(ctx, conf.ServiceConfig.MASAddress, conf.ServiceConfig.WorkerNodes, process.IdentityTol, process.DpTol, errChan)

			if dataSource.BandStrides <= 0 {
				dataSource.BandStrides = 1
			}
			proc := dp.Process(geoReq, suffix, dataSource.MetadataURL, dataSource.BandStrides, *process.Approx, *verbose)

			select {
			case res := <-proc:
				result += res
			case err := <-errChan:
				Info.Printf("Error in the pipeline: %v\n", err)
				http.Error(w, err.Error(), 500)
				return
			case <-ctx.Done():
				Error.Printf("Context cancelled with message: %v\n", ctx.Err())
				http.Error(w, ctx.Err().Error(), 500)
				return
			}
		}

		err = utils.ExecuteWriteTemplateFile(w, result, utils.DataDir+"/templates/WPS_Execute.tpl")
		if err != nil {
			http.Error(w, err.Error(), 500)
		}

	default:
		http.Error(w, fmt.Sprintf("%s not recognised.", *params.Request), 400)
	}
}

// owsHandler handles every request received on /ows
func generalHandler(conf *utils.Config, w http.ResponseWriter, r *http.Request) {
//Info.Printf("%s\n", r.URL.String())
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if *verbose {
		Info.Printf("%s\n", r.URL.String())
	}
	ctx := r.Context()

	var query map[string][]string
	var err error
	switch r.Method {
	case "POST":
		query, err = utils.ParsePost(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Error parsing WPS POST payload: %s", err), 400)
			return
		}

	case "GET":
		query = utils.NormaliseKeys(r.URL.Query())
	}
	if _, fOK := query["service"]; !fOK {
		canInferService := false
		if request, hasReq := query["request"]; hasReq {
			reqService := map[string]string{
				"GetFeatureInfo":   "WMS",
				"GetMap":           "WMS",
				"DescribeLayer":    "WMS",
				"GetLegendGraphic": "WMS",
				"DescribeCoverage": "WCS",
				"GetCoverage":      "WCS",
				"DescribeProcess":  "WPS",
				"Execute":          "WPS",
			}
			if service, found := reqService[request[0]]; found {
				query["service"] = []string{service}
				canInferService = true
			}
		}

		if !canInferService {
			http.Error(w, fmt.Sprintf("Not a OWS request. Request does not contain a 'service' parameter."), 400)
			return
		}
	}

	switch query["service"][0] {
	case "WMS":
		params, err := utils.WMSParamsChecker(query, reWMSMap)
		if err != nil {
			http.Error(w, fmt.Sprintf("Wrong WMS parameters on URL: %s", err), 400)
			return
		}
		serveWMS(ctx, params, conf, r.URL.String(), w, r) // AVS: added ", r"
	case "WCS":
		if (*dap) {
			query["format"][0] = "netcdf" // AVS: Save the file as NetCDF (*.nc) on the server and as HDF5Image/HDF5 on PC (*nc or *.tiff)
		}
		params, err := utils.WCSParamsChecker(query, reWCSMap)

	if err != nil {
			http.Error(w, fmt.Sprintf("Wrong WCS parameters on URL: %s", err), 400)
			return
		}
		serveWCS(ctx, params, conf, r.URL.String(), w, query)
	case "WPS":
		params, err := utils.WPSParamsChecker(query, reWPSMap)
		if err != nil {
			http.Error(w, fmt.Sprintf("Wrong WPS parameters on URL: %s", err), 400)
			return
		}
		serveWPS(ctx, params, conf, r.URL.String(), w)
	default:
		http.Error(w, fmt.Sprintf("Not a valid OWS request. URL %s does not contain a valid 'request' parameter.", r.URL.String()), 400)
		return
	}
}

func owsHandler(w http.ResponseWriter, r *http.Request) {
//fmt.Println(r.URL.Path)		
	namespace := "."
	if len(r.URL.Path) > len("/ows/") {
		namespace = r.URL.Path[len("/ows/"):]
	}
//Info.Printf("%s\n", r.URL)
//Info.Printf("%s\n", namespace)
	config, ok := configMap[namespace]
//PU(configMap[config])		
	if !ok {
		Info.Printf("Invalid dataset namespace: %v for url: %v\n", namespace, r.URL.Path)
		http.Error(w, fmt.Sprintf("Invalid dataset namespace: %v\n", namespace), 404)
		return
	}
	config.ServiceConfig.NameSpace = namespace
	generalHandler(config, w, r)
}

func main() {
//fmt.Println("In Main:")		
	fs := http.FileServer(http.Dir(utils.DataDir + "/static"))
//fmt.Println(fs)		
	http.Handle("/", fs)
	http.HandleFunc("/ows", owsHandler)
	http.HandleFunc("/ows/", owsHandler)
	Info.Printf("GSKY is ready")
	log.Fatal(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%d", *port), nil))
}
