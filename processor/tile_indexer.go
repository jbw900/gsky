package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	
	"strconv"
//  "reflect"
	"os/exec"
	"os"
	"regexp"
)
var	ThreddsDataDir = "/usr/local/tds/apache-tomcat-8.5.35/content/thredds/public/gsky/"

type GDALDataset struct {
	DSName       string      `json:"ds_name"`
	NameSpace    string      `json:"namespace"`
	ArrayType    string      `json:"array_type"`
	TimeStamps   []time.Time `json:"timestamps"`
	Polygon      string      `json:"polygon"`
	Means        []float64   `json:"means"`
	SampleCounts []int       `json:"sample_counts"`
	NoData       float64     `json:"nodata"`
}

type MetadataResponse struct {
	Error        string        `json:"error"`
	Files        []string      `json:"files"`
	GDALDatasets []GDALDataset `json:"gdal"`
}

type TileIndexer struct {
	Context    context.Context
	In         chan *GeoTileRequest
	Out        chan *GeoTileGranule
	Error      chan error
	APIAddress string
	QueryLimit int
}

func NewTileIndexer(ctx context.Context, apiAddr string, errChan chan error) *TileIndexer {
	return &TileIndexer{
		Context:    ctx,
		In:         make(chan *GeoTileRequest, 100),
		Out:        make(chan *GeoTileGranule, 100),
		Error:      errChan,
		APIAddress: apiAddr,
	}
}

func BBox2WKT(bbox []float64) string {
	// BBox xMin, yMin, xMax, yMax
	return fmt.Sprintf("POLYGON ((%f %f, %f %f, %f %f, %f %f, %f %f))", bbox[0], bbox[1], bbox[2], bbox[1], bbox[2], bbox[3], bbox[0], bbox[3], bbox[0], bbox[1])
}

func (p *TileIndexer) Run(verbose bool) {
	if verbose {
		defer log.Printf("tile indexer done")
	}
	defer close(p.Out)

	for geoReq := range p.In {
		select {
		case <-p.Context.Done():
			p.Error <- fmt.Errorf("Tile indexer context has been cancel: %v", p.Context.Err())
			return
		default:
			var wg sync.WaitGroup
			var url string

			if len(geoReq.NameSpaces) == 0 {
				geoReq.NameSpaces = append(geoReq.NameSpaces, "")
			}

			nameSpaces := strings.Join(geoReq.NameSpaces, ",")
			if geoReq.EndTime == nil {
				url = strings.Replace(fmt.Sprintf("http://%s%s?intersects&metadata=gdal&time=%s&srs=%s&wkt=%s&namespace=%s&nseg=%d&limit=%d", p.APIAddress, geoReq.Collection, geoReq.StartTime.Format(ISOFormat), geoReq.CRS, BBox2WKT(geoReq.BBox), nameSpaces, geoReq.PolygonSegments, geoReq.QueryLimit), " ", "%20", -1)
			} else {
				url = strings.Replace(fmt.Sprintf("http://%s%s?intersects&metadata=gdal&time=%s&until=%s&srs=%s&wkt=%s&namespace=%s&nseg=%d&limit=%d", p.APIAddress, geoReq.Collection, geoReq.StartTime.Format(ISOFormat), geoReq.EndTime.Format(ISOFormat), geoReq.CRS, BBox2WKT(geoReq.BBox), nameSpaces, geoReq.PolygonSegments, geoReq.QueryLimit), " ", "%20", -1)
			}
			if verbose {
				log.Println(url)
			}

			wg.Add(1)
			go URLIndexGet(p.Context, url, geoReq, p.Error, p.Out, &wg)
			if geoReq.Mask != nil {
				maskCollection := geoReq.Mask.DataSource
				if len(maskCollection) == 0 {
					maskCollection = geoReq.Collection
				}

				if maskCollection != geoReq.Collection || geoReq.Mask.ID != nameSpaces {
					if geoReq.EndTime == nil {
						url = strings.Replace(fmt.Sprintf("http://%s%s?intersects&metadata=gdal&time=%s&srs=%s&wkt=%s&namespace=%s&nseg=%d&limit=%d", p.APIAddress, maskCollection, geoReq.StartTime.Format(ISOFormat), geoReq.CRS, BBox2WKT(geoReq.BBox), geoReq.Mask.ID, geoReq.PolygonSegments, geoReq.QueryLimit), " ", "%20", -1)
					} else {
						url = strings.Replace(fmt.Sprintf("http://%s%s?intersects&metadata=gdal&time=%s&until=%s&srs=%s&wkt=%s&namespace=%s&nseg=%d&limit=%d", p.APIAddress, maskCollection, geoReq.StartTime.Format(ISOFormat), geoReq.EndTime.Format(ISOFormat), geoReq.CRS, BBox2WKT(geoReq.BBox), geoReq.Mask.ID, geoReq.PolygonSegments, geoReq.QueryLimit), " ", "%20", -1)
					}
					if verbose {
						log.Println(url)
					}

					wg.Add(1)
					go URLIndexGet(p.Context, url, geoReq, p.Error, p.Out, &wg)
				}
			}
			wg.Wait()
		}
	}
}
// AVS: Function to connect GSKY to THredds
func delete_thredds_nc() {
    thredds_last := ThreddsDataDir + "thredds_last"
    thredds_nc := ThreddsDataDir + "*.nc"
	timestamp, _ := ioutil.ReadFile(thredds_last)
	timestamp_int, _ := strconv.Atoi(string(timestamp))
	et := time.Now().Unix() - int64(timestamp_int)
	/*
	 A single zoom action sends several http requests within 1 sec. Must not 
	 delete the NC files between those requests. If the last access was >= 1 sec, 
	 we can delete the previous NC links. There is a risk that previous NC files 
	 may not be deleted if the user consecutively zooms within 2 sec. 
	*/
	if (et > 2) {
		fmt.Println("-- Deleting the previous soft links...")		
		f, _ := os.Create(thredds_last)
		defer f.Close()
		now := strconv.FormatInt(time.Now().Unix(), 10)
		f.WriteString(now)
		rm_thredds_nc := "rm -f " + thredds_nc
		exec.Command("/bin/sh", "-c", rm_thredds_nc).CombinedOutput()
	}
	fmt.Printf("++ Adding soft links to the NC files. Some could be duplicates...\n")
}
func add_thredds_nc (ds GDALDataset) {
    r := regexp.MustCompile(`(?P<Type>.*):"(?P<File>.*)":(?P<NameSpace>.*)`)
    m := r.FindStringSubmatch(ds.DSName)
	exec.Command("ln", "-s", m[2], ThreddsDataDir).CombinedOutput()
}
func URLIndexGet(ctx context.Context, url string, geoReq *GeoTileRequest, errChan chan error, out chan *GeoTileGranule, wg *sync.WaitGroup) {
	defer wg.Done()

	resp, err := http.Get(url)
	if err != nil {
		errChan <- fmt.Errorf("GET request to %s failed. Error: %v", url, err)
		out <- &GeoTileGranule{ConfigPayLoad: ConfigPayLoad{NameSpaces: []string{"EmptyTile"}, ScaleParams: geoReq.ScaleParams, Palette: geoReq.Palette}, Path: "NULL", NameSpace: "EmptyTile", RasterType: "Byte", TimeStamps: nil, TimeStamp: *geoReq.StartTime, BBox: geoReq.BBox, Height: geoReq.Height, Width: geoReq.Width, OffX: geoReq.OffX, OffY: geoReq.OffY, CRS: geoReq.CRS}
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		errChan <- fmt.Errorf("Error parsing response body from %s. Error: %v", url, err)
		out <- &GeoTileGranule{ConfigPayLoad: ConfigPayLoad{NameSpaces: []string{"EmptyTile"}, ScaleParams: geoReq.ScaleParams, Palette: geoReq.Palette}, Path: "NULL", NameSpace: "EmptyTile", RasterType: "Byte", TimeStamps: nil, TimeStamp: *geoReq.StartTime, BBox: geoReq.BBox, Height: geoReq.Height, Width: geoReq.Width, OffX: geoReq.OffX, OffY: geoReq.OffY, CRS: geoReq.CRS}
		return
	}
	var metadata MetadataResponse
	err = json.Unmarshal(body, &metadata)
	if err != nil {
		errChan <- fmt.Errorf("Problem parsing JSON response from %s. Error: %v", url, err)
		out <- &GeoTileGranule{ConfigPayLoad: ConfigPayLoad{NameSpaces: []string{"EmptyTile"}, ScaleParams: geoReq.ScaleParams, Palette: geoReq.Palette}, Path: "NULL", NameSpace: "EmptyTile", RasterType: "Byte", TimeStamps: nil, TimeStamp: *geoReq.StartTime, BBox: geoReq.BBox, Height: geoReq.Height, Width: geoReq.Width, OffX: geoReq.OffX, OffY: geoReq.OffY, CRS: geoReq.CRS}
		return
	}

	switch len(metadata.GDALDatasets) {
	case 0:
		if len(metadata.Error) > 0 {
			log.Printf("Indexer returned error: %v", string(body))
			errChan <- fmt.Errorf("Indexer returned error: %v", metadata.Error)
		}
		out <- &GeoTileGranule{ConfigPayLoad: ConfigPayLoad{NameSpaces: []string{"EmptyTile"}, ScaleParams: geoReq.ScaleParams, Palette: geoReq.Palette}, Path: "NULL", NameSpace: "EmptyTile", RasterType: "Byte", TimeStamps: nil, TimeStamp: *geoReq.StartTime, BBox: geoReq.BBox, Height: geoReq.Height, Width: geoReq.Width, OffX: geoReq.OffX, OffY: geoReq.OffY, CRS: geoReq.CRS}
	default:
		delete_thredds_nc() // AVS: delete the *.nc files from 'thredds_dir' if this is a new call.
		for _, ds := range metadata.GDALDatasets {
			add_thredds_nc(ds) // AVS: Code to get the NC filenames for Thredds and create soft links in 'thredds_dir'
			for _, t := range ds.TimeStamps {
				if t.Equal(*geoReq.StartTime) || geoReq.EndTime != nil && t.After(*geoReq.StartTime) && t.Before(*geoReq.EndTime) {
					out <- &GeoTileGranule{ConfigPayLoad: ConfigPayLoad{NameSpaces: geoReq.NameSpaces, Mask: geoReq.Mask, ScaleParams: geoReq.ScaleParams, Palette: geoReq.Palette, GrpcConcLimit: geoReq.GrpcConcLimit}, Path: ds.DSName, NameSpace: ds.NameSpace, RasterType: ds.ArrayType, TimeStamps: ds.TimeStamps, TimeStamp: t, Polygon: ds.Polygon, BBox: geoReq.BBox, Height: geoReq.Height, Width: geoReq.Width, OffX: geoReq.OffX, OffY: geoReq.OffY, CRS: geoReq.CRS}
				}
			}
		}
	}
}
