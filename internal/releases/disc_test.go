package releases

import "testing"

func TestFullDiscRelease(t *testing.T) {
	discs := []string{
		"Some.Movie.2001.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-GRP",
		"Some.Movie.2001.1080p.Blu-ray.DTS-HD.MA.2.0",
		"Some Movie 2001 1080p USA Blu-ray VC-1 DTS-HD MA 5.1",
		"Some.Movie.2001.1080p.BluRay.AVC.LPCM.1.0-GRP",
		"Some.Movie.2001.2160p.UHD.Blu-ray.DV.HDR.HEVC.TrueHD.Atmos.7.1",
		"Some.Movie.2001.2160p.UHD.BluRay.HEVC.TrueHD-GRP",
		"Some.Movie.2001.NORDiC.COMPLETE.BLURAY-GRP",
		"Some.Movie.2001.COMPLETE.UHD.BLURAY-GRP",
		"Some.Movie.2001.1080p.BluRay.MVC.DTS-HDMA-GRP",
		"Some.Movie.2001.German.DL.1080p.Untouched.BluRay-GRP",
		"Some.Movie.2001.NTSC.DVD5.DD2.0",
		"Some.Movie.2001.WIDESCREEN.NTSC.USA.DVD9-GRP",
		"Some.Movie.2001.PAL.DVDR-GRP",
		"Some.Movie.2001.BDMV-GRP",
		"Some.Movie.2001.BD50-GRP",
		"Some.Movie.2001.VIDEO_TS",
		"Some Movie 2001 ISO",
	}
	for _, title := range discs {
		if !FullDiscRelease(title) {
			t.Errorf("%q is a full disc", title)
		}
	}
	files := []string{
		"Some.Movie.2001.1080p.BluRay.x264-GRP",
		"Some.Movie.2001.1080p.BluRay.H.264.DTS-HD.MA.5.1-GRP",
		"Some.Movie.2001.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
		"Some.Movie.2001.1080p.Blu-ray.RemuxAVC.DTS-HD.MA-GRP",
		"Some.Movie.2001.1080p.Blu-ray.x265.10bit-GRP",
		"Some.Movie.2001.NORDiC.1080p.HDR.BluRay.HEVC.TrueHD.7.1-GRP",
		"Some.Movie.2001.1080p.BluRay.HDR10.HEVC.10bit.DTS-HD.MA-GRP",
		"Some.Movie.2001.German.DL.1080p.BluRay.VC1-GRP",
		"Some.Movie.2001.1080p.Bluray.Half-SBS.DTSHD-MA.5.1-GRP",
		"Some.Movie.2001.DANiSH.720p.DVDr.H.264.DD2.0-GRP",
		"Some.Movie.2001.DVDRip.XviD-GRP",
		"Some.Movie.2001.1080p.BDRip.x264-GRP",
		"Some.Movie.2001.1080p.WEB-DL.AVC.DDP5.1-GRP",
		"Some.Movie.2001.2160p.UHD.BluRay.x265.HDR-GRP",
		"Isolation.2005.1080p.BluRay.x264-GRP",
		"Some.Movie.2001.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP",
	}
	for _, title := range files {
		if FullDiscRelease(title) {
			t.Errorf("%q is not a full disc", title)
		}
	}
}
