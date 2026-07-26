package minecraftlocale

import "strings"

var supported = map[string]bool{
	"af_za": true, "ar_sa": true, "ast_es": true, "az_az": true,
	"ba_ru": true, "bar": true, "be_by": true, "bg_bg": true,
	"br_fr": true, "brb": true, "bs_ba": true, "ca_es": true,
	"cs_cz": true, "cy_gb": true, "da_dk": true, "de_at": true,
	"de_ch": true, "de_de": true, "el_gr": true, "en_au": true,
	"en_ca": true, "en_gb": true, "en_nz": true, "en_pt": true,
	"en_ud": true, "en_us": true, "enp": true, "enws": true,
	"eo_uy": true, "es_ar": true, "es_cl": true, "es_ec": true,
	"es_es": true, "es_mx": true, "es_uy": true, "es_ve": true,
	"et_ee": true, "eu_es": true, "fa_ir": true, "fi_fi": true,
	"fil_ph": true, "fo_fo": true, "fr_ca": true, "fr_fr": true,
	"fra_de": true, "fur_it": true, "fy_nl": true, "ga_ie": true,
	"gd_gb": true, "gl_es": true, "haw_us": true, "he_il": true,
	"hi_in": true, "hr_hr": true, "hu_hu": true, "hy_am": true,
	"id_id": true, "ig_ng": true, "io_en": true, "is_is": true,
	"isv": true, "it_it": true, "ja_jp": true, "jbo_en": true,
	"ka_ge": true, "kk_kz": true, "kn_in": true, "ko_kr": true,
	"ksh": true, "kw_gb": true, "la_la": true, "lb_lu": true,
	"li_li": true, "lmo": true, "lol_us": true, "lt_lt": true,
	"lv_lv": true, "lzh": true, "mk_mk": true, "mn_mn": true,
	"moh_ca": true, "ms_my": true, "mt_mt": true, "nah": true,
	"nds_de": true, "nl_be": true, "nl_nl": true, "nn_no": true,
	"no_no": true, "oc_fr": true, "oj_ca": true, "ovd": true,
	"pl_pl": true, "pt_br": true, "pt_pt": true, "qya_aa": true,
	"ro_ro": true, "rpr": true, "ru_ru": true, "ry_ua": true,
	"se_no": true, "sk_sk": true, "sl_si": true, "so_so": true,
	"sq_al": true, "sr_sp": true, "sv_se": true, "sxu": true,
	"szl": true, "ta_in": true, "th_th": true, "tl_ph": true,
	"tlh_aa": true, "tok": true, "tr_tr": true, "tt_ru": true,
	"uk_ua": true, "val_es": true, "vec_it": true, "vi_vn": true,
	"yi_de": true, "yo_ng": true, "zh_cn": true, "zh_hk": true,
	"zh_tw": true, "zlm_arab": true,
}

func Normalize(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if index := strings.IndexAny(value, ".@"); index >= 0 {
		value = value[:index]
	}
	value = strings.ReplaceAll(value, "-", "_")
	locale := strings.ToLower(value)
	if !supported[locale] {
		return "", false
	}
	return locale, true
}
