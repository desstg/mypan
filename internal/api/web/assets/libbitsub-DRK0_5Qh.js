/* @ts-self-types="./libbitsub.d.ts" */

/**
 * DVB subtitle parser and renderer exposed to JavaScript.
 */
export class DvbParser {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        DvbParserFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_dvbparser_free(ptr, 0);
    }
    clearCache() {
        wasm.dvbparser_clearCache(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get count() {
        const ret = wasm.dvbparser_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    feed(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.dvbparser_feed(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @param {number} time_ms
     * @returns {number}
     */
    findIndexAtTimestamp(time_ms) {
        const ret = wasm.dvbparser_findIndexAtTimestamp(this.__wbg_ptr, time_ms);
        return ret;
    }
    /**
     * @returns {number}
     */
    finishFeed() {
        const ret = wasm.dvbparser_finishFeed(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueCompositionCount(index) {
        const ret = wasm.dvbparser_getCueCompositionCount(this.__wbg_ptr, index);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueEndTime(index) {
        const ret = wasm.dvbparser_getCueEndTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCuePageState(index) {
        const ret = wasm.dvbparser_getCuePageState(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueStartTime(index) {
        const ret = wasm.dvbparser_getCueStartTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @returns {Float64Array}
     */
    getEndTimestamps() {
        const ret = wasm.dvbparser_getEndTimestamps(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {Float64Array}
     */
    getTimestamps() {
        const ret = wasm.dvbparser_getTimestamps(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {string}
     */
    get lastRenderIssue() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.dvbparser_lastRenderIssue(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    constructor() {
        const ret = wasm.dvbparser_new();
        this.__wbg_ptr = ret;
        DvbParserFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    parse(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.dvbparser_parse(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @returns {number}
     */
    get pendingLen() {
        const ret = wasm.dvbparser_pendingLen(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {SubtitleFrame | undefined}
     */
    renderAtIndex(index) {
        const ret = wasm.dvbparser_renderAtIndex(this.__wbg_ptr, index);
        return ret === 0 ? undefined : SubtitleFrame.__wrap(ret);
    }
    reset() {
        wasm.dvbparser_reset(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.dvbparser_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.dvbparser_screenWidth(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) DvbParser.prototype[Symbol.dispose] = DvbParser.prototype.free;

/**
 * PGS subtitle parser and renderer exposed to JavaScript.
 */
export class PgsParser {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        PgsParserFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_pgsparser_free(ptr, 0);
    }
    clearCache() {
        wasm.pgsparser_clearCache(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get count() {
        const ret = wasm.pgsparser_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    feed(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.pgsparser_feed(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @param {number} time_ms
     * @returns {number}
     */
    findIndexAtTimestamp(time_ms) {
        const ret = wasm.pgsparser_findIndexAtTimestamp(this.__wbg_ptr, time_ms);
        return ret;
    }
    /**
     * @returns {number}
     */
    finishFeed() {
        const ret = wasm.pgsparser_finishFeed(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueCompositionCount(index) {
        const ret = wasm.pgsparser_getCueCompositionCount(this.__wbg_ptr, index);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueCompositionState(index) {
        const ret = wasm.pgsparser_getCueCompositionState(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueEndTime(index) {
        const ret = wasm.pgsparser_getCueEndTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCuePaletteId(index) {
        const ret = wasm.pgsparser_getCuePaletteId(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueStartTime(index) {
        const ret = wasm.pgsparser_getCueStartTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @returns {Float64Array}
     */
    getTimestamps() {
        const ret = wasm.pgsparser_getTimestamps(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {string}
     */
    get lastRenderIssue() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.pgsparser_lastRenderIssue(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    constructor() {
        const ret = wasm.pgsparser_new();
        this.__wbg_ptr = ret;
        PgsParserFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    parse(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.pgsparser_parse(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @returns {number}
     */
    get pendingLen() {
        const ret = wasm.pgsparser_pendingLen(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {SubtitleFrame | undefined}
     */
    renderAtIndex(index) {
        const ret = wasm.pgsparser_renderAtIndex(this.__wbg_ptr, index);
        return ret === 0 ? undefined : SubtitleFrame.__wrap(ret);
    }
    reset() {
        wasm.pgsparser_reset(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.pgsparser_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.pgsparser_screenWidth(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) PgsParser.prototype[Symbol.dispose] = PgsParser.prototype.free;

/**
 * Unified render result for both formats.
 */
export class RenderResult {
    static __wrap(ptr) {
        const obj = Object.create(RenderResult.prototype);
        obj.__wbg_ptr = ptr;
        RenderResultFinalization.register(obj, obj.__wbg_ptr, obj);
        return obj;
    }
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        RenderResultFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_renderresult_free(ptr, 0);
    }
    /**
     * @returns {number}
     */
    get compositionCount() {
        const ret = wasm.renderresult_compositionCount(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCompositionHeight(index) {
        const ret = wasm.renderresult_getCompositionHeight(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {Uint8Array}
     */
    getCompositionRgba(index) {
        const ret = wasm.renderresult_getCompositionRgba(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCompositionWidth(index) {
        const ret = wasm.renderresult_getCompositionWidth(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCompositionX(index) {
        const ret = wasm.renderresult_getCompositionX(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCompositionY(index) {
        const ret = wasm.renderresult_getCompositionY(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.renderresult_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.renderresult_screenWidth(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) RenderResult.prototype[Symbol.dispose] = RenderResult.prototype.free;

/**
 * A single PGS subtitle composition element.
 */
export class SubtitleComposition {
    static __wrap(ptr) {
        const obj = Object.create(SubtitleComposition.prototype);
        obj.__wbg_ptr = ptr;
        SubtitleCompositionFinalization.register(obj, obj.__wbg_ptr, obj);
        return obj;
    }
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        SubtitleCompositionFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_subtitlecomposition_free(ptr, 0);
    }
    /**
     * @returns {Uint8Array}
     */
    getRgba() {
        const ret = wasm.subtitlecomposition_getRgba(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get height() {
        const ret = wasm.subtitlecomposition_height(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get width() {
        const ret = wasm.subtitlecomposition_width(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get x() {
        const ret = wasm.subtitlecomposition_x(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get y() {
        const ret = wasm.subtitlecomposition_y(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) SubtitleComposition.prototype[Symbol.dispose] = SubtitleComposition.prototype.free;

/**
 * Subtitle format type.
 * @enum {0 | 1 | 2}
 */
export const SubtitleFormat = Object.freeze({
    Pgs: 0, "0": "Pgs",
    VobSub: 1, "1": "VobSub",
    Dvb: 2, "2": "Dvb",
});

/**
 * A complete PGS subtitle frame with all compositions.
 */
export class SubtitleFrame {
    static __wrap(ptr) {
        const obj = Object.create(SubtitleFrame.prototype);
        obj.__wbg_ptr = ptr;
        SubtitleFrameFinalization.register(obj, obj.__wbg_ptr, obj);
        return obj;
    }
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        SubtitleFrameFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_subtitleframe_free(ptr, 0);
    }
    /**
     * @returns {number}
     */
    get compositionCount() {
        const ret = wasm.subtitleframe_compositionCount(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @param {number} index
     * @returns {SubtitleComposition | undefined}
     */
    getComposition(index) {
        const ret = wasm.subtitleframe_getComposition(this.__wbg_ptr, index);
        return ret === 0 ? undefined : SubtitleComposition.__wrap(ret);
    }
    /**
     * @returns {number}
     */
    get height() {
        const ret = wasm.subtitleframe_height(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get width() {
        const ret = wasm.subtitleframe_width(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) SubtitleFrame.prototype[Symbol.dispose] = SubtitleFrame.prototype.free;

/**
 * Unified subtitle renderer for PGS, VobSub, and DVB formats.
 */
export class SubtitleRenderer {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        SubtitleRendererFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_subtitlerenderer_free(ptr, 0);
    }
    clearCache() {
        wasm.subtitlerenderer_clearCache(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get count() {
        const ret = wasm.subtitlerenderer_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    dispose() {
        wasm.subtitlerenderer_dispose(this.__wbg_ptr);
    }
    /**
     * @param {number} time_ms
     * @returns {number}
     */
    findIndexAtTimestamp(time_ms) {
        const ret = wasm.subtitlerenderer_findIndexAtTimestamp(this.__wbg_ptr, time_ms);
        return ret;
    }
    /**
     * @returns {SubtitleFormat | undefined}
     */
    get format() {
        const ret = wasm.subtitlerenderer_format(this.__wbg_ptr);
        return ret === 3 ? undefined : ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueDuration(index) {
        const ret = wasm.subtitlerenderer_getCueDuration(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueEndTime(index) {
        const ret = wasm.subtitlerenderer_getCueEndTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueStartTime(index) {
        const ret = wasm.subtitlerenderer_getCueStartTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @returns {Float64Array}
     */
    getTimestamps() {
        const ret = wasm.subtitlerenderer_getTimestamps(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {boolean}
     */
    get hasIdxMetadata() {
        const ret = wasm.subtitlerenderer_hasIdxMetadata(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * @returns {string}
     */
    get language() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.subtitlerenderer_language(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    /**
     * @returns {string}
     */
    get lastRenderIssue() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.subtitlerenderer_lastRenderIssue(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    loadDvb(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.subtitlerenderer_loadDvb(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @param {Uint8Array} data
     * @returns {number}
     */
    loadPgs(data) {
        const ptr0 = passArray8ToWasm0(data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.subtitlerenderer_loadPgs(this.__wbg_ptr, ptr0, len0);
        return ret >>> 0;
    }
    /**
     * @param {string} idx_content
     * @param {Uint8Array} sub_data
     */
    loadVobSub(idx_content, sub_data) {
        const ptr0 = passStringToWasm0(idx_content, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
        const len0 = WASM_VECTOR_LEN;
        const ptr1 = passArray8ToWasm0(sub_data, wasm.__wbindgen_malloc);
        const len1 = WASM_VECTOR_LEN;
        wasm.subtitlerenderer_loadVobSub(this.__wbg_ptr, ptr0, len0, ptr1, len1);
    }
    /**
     * @param {Uint8Array} mks_data
     */
    loadVobSubMks(mks_data) {
        const ptr0 = passArray8ToWasm0(mks_data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.subtitlerenderer_loadVobSubMks(this.__wbg_ptr, ptr0, len0);
        if (ret[1]) {
            throw takeFromExternrefTable0(ret[0]);
        }
    }
    /**
     * @param {Uint8Array} sub_data
     */
    loadVobSubOnly(sub_data) {
        const ptr0 = passArray8ToWasm0(sub_data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        wasm.subtitlerenderer_loadVobSubOnly(this.__wbg_ptr, ptr0, len0);
    }
    constructor() {
        const ret = wasm.subtitlerenderer_new();
        this.__wbg_ptr = ret;
        SubtitleRendererFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {number} index
     * @returns {RenderResult | undefined}
     */
    renderAtIndex(index) {
        const ret = wasm.subtitlerenderer_renderAtIndex(this.__wbg_ptr, index);
        return ret === 0 ? undefined : RenderResult.__wrap(ret);
    }
    /**
     * @param {number} time_seconds
     * @returns {RenderResult | undefined}
     */
    renderAtTimestamp(time_seconds) {
        const ret = wasm.subtitlerenderer_renderAtTimestamp(this.__wbg_ptr, time_seconds);
        return ret === 0 ? undefined : RenderResult.__wrap(ret);
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.subtitlerenderer_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.subtitlerenderer_screenWidth(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {string}
     */
    get trackId() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.subtitlerenderer_trackId(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
}
if (Symbol.dispose) SubtitleRenderer.prototype[Symbol.dispose] = SubtitleRenderer.prototype.free;

/**
 * A VobSub subtitle frame.
 */
export class VobSubFrame {
    static __wrap(ptr) {
        const obj = Object.create(VobSubFrame.prototype);
        obj.__wbg_ptr = ptr;
        VobSubFrameFinalization.register(obj, obj.__wbg_ptr, obj);
        return obj;
    }
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        VobSubFrameFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_vobsubframe_free(ptr, 0);
    }
    /**
     * @returns {Uint8Array}
     */
    getRgba() {
        const ret = wasm.vobsubframe_getRgba(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get height() {
        const ret = wasm.vobsubframe_height(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.vobsubframe_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.vobsubframe_screenWidth(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get width() {
        const ret = wasm.vobsubframe_width(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get x() {
        const ret = wasm.vobsubframe_x(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get y() {
        const ret = wasm.vobsubframe_y(this.__wbg_ptr);
        return ret;
    }
}
if (Symbol.dispose) VobSubFrame.prototype[Symbol.dispose] = VobSubFrame.prototype.free;

/**
 * VobSub subtitle parser and renderer exposed to JavaScript.
 */
export class VobSubParser {
    __destroy_into_raw() {
        const ptr = this.__wbg_ptr;
        this.__wbg_ptr = 0;
        VobSubParserFinalization.unregister(this);
        return ptr;
    }
    free() {
        const ptr = this.__destroy_into_raw();
        wasm.__wbg_vobsubparser_free(ptr, 0);
    }
    /**
     * @param {Uint8Array} sub_data
     */
    attachSubData(sub_data) {
        const ptr0 = passArray8ToWasm0(sub_data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        wasm.vobsubparser_attachSubData(this.__wbg_ptr, ptr0, len0);
    }
    clearCache() {
        wasm.vobsubparser_clearCache(this.__wbg_ptr);
    }
    /**
     * @returns {number}
     */
    get count() {
        const ret = wasm.vobsubparser_count(this.__wbg_ptr);
        return ret >>> 0;
    }
    /**
     * @returns {boolean}
     */
    get debandEnabled() {
        const ret = wasm.vobsubparser_debandEnabled(this.__wbg_ptr);
        return ret !== 0;
    }
    dispose() {
        wasm.vobsubparser_dispose(this.__wbg_ptr);
    }
    /**
     * @param {number} time_ms
     * @returns {number}
     */
    findIndexAtTimestamp(time_ms) {
        const ret = wasm.vobsubparser_findIndexAtTimestamp(this.__wbg_ptr, time_ms);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueDuration(index) {
        const ret = wasm.vobsubparser_getCueDuration(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueEndTime(index) {
        const ret = wasm.vobsubparser_getCueEndTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueFilePosition(index) {
        const ret = wasm.vobsubparser_getCueFilePosition(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @param {number} index
     * @returns {number}
     */
    getCueStartTime(index) {
        const ret = wasm.vobsubparser_getCueStartTime(this.__wbg_ptr, index);
        return ret;
    }
    /**
     * @returns {Float64Array}
     */
    getTimestamps() {
        const ret = wasm.vobsubparser_getTimestamps(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {boolean}
     */
    get hasIdxMetadata() {
        const ret = wasm.vobsubparser_hasIdxMetadata(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * @returns {boolean}
     */
    get hasSubData() {
        const ret = wasm.vobsubparser_hasSubData(this.__wbg_ptr);
        return ret !== 0;
    }
    /**
     * @returns {string}
     */
    get language() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.vobsubparser_language(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    /**
     * @returns {string}
     */
    get lastRenderIssue() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.vobsubparser_lastRenderIssue(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
    /**
     * @param {string} idx_content
     * @param {Uint8Array} sub_data
     */
    loadFromData(idx_content, sub_data) {
        const ptr0 = passStringToWasm0(idx_content, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
        const len0 = WASM_VECTOR_LEN;
        const ptr1 = passArray8ToWasm0(sub_data, wasm.__wbindgen_malloc);
        const len1 = WASM_VECTOR_LEN;
        wasm.vobsubparser_loadFromData(this.__wbg_ptr, ptr0, len0, ptr1, len1);
    }
    /**
     * @param {string} idx_content
     */
    loadFromIdx(idx_content) {
        const ptr0 = passStringToWasm0(idx_content, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
        const len0 = WASM_VECTOR_LEN;
        wasm.vobsubparser_loadFromIdx(this.__wbg_ptr, ptr0, len0);
    }
    /**
     * @param {Uint8Array} mks_data
     */
    loadFromMks(mks_data) {
        const ptr0 = passArray8ToWasm0(mks_data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        const ret = wasm.vobsubparser_loadFromMks(this.__wbg_ptr, ptr0, len0);
        if (ret[1]) {
            throw takeFromExternrefTable0(ret[0]);
        }
    }
    /**
     * @param {Uint8Array} sub_data
     */
    loadFromSubOnly(sub_data) {
        const ptr0 = passArray8ToWasm0(sub_data, wasm.__wbindgen_malloc);
        const len0 = WASM_VECTOR_LEN;
        wasm.vobsubparser_loadFromSubOnly(this.__wbg_ptr, ptr0, len0);
    }
    constructor() {
        const ret = wasm.vobsubparser_new();
        this.__wbg_ptr = ret;
        VobSubParserFinalization.register(this, this.__wbg_ptr, this);
        return this;
    }
    /**
     * @param {number} index
     * @returns {VobSubFrame | undefined}
     */
    renderAtIndex(index) {
        const ret = wasm.vobsubparser_renderAtIndex(this.__wbg_ptr, index);
        return ret === 0 ? undefined : VobSubFrame.__wrap(ret);
    }
    /**
     * @returns {number}
     */
    get screenHeight() {
        const ret = wasm.vobsubparser_screenHeight(this.__wbg_ptr);
        return ret;
    }
    /**
     * @returns {number}
     */
    get screenWidth() {
        const ret = wasm.vobsubparser_screenWidth(this.__wbg_ptr);
        return ret;
    }
    /**
     * @param {boolean} enabled
     */
    setDebandEnabled(enabled) {
        wasm.vobsubparser_setDebandEnabled(this.__wbg_ptr, enabled);
    }
    /**
     * @param {number} range
     */
    setDebandRange(range) {
        wasm.vobsubparser_setDebandRange(this.__wbg_ptr, range);
    }
    /**
     * @param {number} threshold
     */
    setDebandThreshold(threshold) {
        wasm.vobsubparser_setDebandThreshold(this.__wbg_ptr, threshold);
    }
    /**
     * @returns {string}
     */
    get trackId() {
        let deferred1_0;
        let deferred1_1;
        try {
            const ret = wasm.vobsubparser_trackId(this.__wbg_ptr);
            deferred1_0 = ret[0];
            deferred1_1 = ret[1];
            return getStringFromWasm0(ret[0], ret[1]);
        } finally {
            wasm.__wbindgen_free(deferred1_0, deferred1_1, 1);
        }
    }
}
if (Symbol.dispose) VobSubParser.prototype[Symbol.dispose] = VobSubParser.prototype.free;

/**
 * Initialize the WASM module. Call this once before using other functions.
 */
export function init() {
    wasm.init();
}
function __wbg_get_imports() {
    const import0 = {
        __proto__: null,
        __wbg___wbindgen_throw_1506f2235d1bdba0: function(arg0, arg1) {
            throw new Error(getStringFromWasm0(arg0, arg1));
        },
        __wbg_error_a6fa202b58aa1cd3: function(arg0, arg1) {
            let deferred0_0;
            let deferred0_1;
            try {
                deferred0_0 = arg0;
                deferred0_1 = arg1;
                console.error(getStringFromWasm0(arg0, arg1));
            } finally {
                wasm.__wbindgen_free(deferred0_0, deferred0_1, 1);
            }
        },
        __wbg_new_227d7c05414eb861: function() {
            const ret = new Error();
            return ret;
        },
        __wbg_new_from_slice_18fa1f71286d66b8: function(arg0, arg1) {
            const ret = new Uint8Array(getArrayU8FromWasm0(arg0, arg1));
            return ret;
        },
        __wbg_new_with_length_36a4998e27b014c5: function(arg0) {
            const ret = new Uint8Array(arg0 >>> 0);
            return ret;
        },
        __wbg_new_with_length_b4a87ccced374381: function(arg0) {
            const ret = new Float64Array(arg0 >>> 0);
            return ret;
        },
        __wbg_set_index_c69336ea758c0507: function(arg0, arg1, arg2) {
            arg0[arg1 >>> 0] = arg2;
        },
        __wbg_stack_3b0d974bbf31e44f: function(arg0, arg1) {
            const ret = arg1.stack;
            const ptr1 = passStringToWasm0(ret, wasm.__wbindgen_malloc, wasm.__wbindgen_realloc);
            const len1 = WASM_VECTOR_LEN;
            getDataViewMemory0().setInt32(arg0 + 4 * 1, len1, true);
            getDataViewMemory0().setInt32(arg0 + 4 * 0, ptr1, true);
        },
        __wbindgen_cast_0000000000000001: function(arg0, arg1) {
            // Cast intrinsic for `Ref(String) -> Externref`.
            const ret = getStringFromWasm0(arg0, arg1);
            return ret;
        },
        __wbindgen_init_externref_table: function() {
            const table = wasm.__wbindgen_externrefs;
            const offset = table.grow(4);
            table.set(0, undefined);
            table.set(offset + 0, undefined);
            table.set(offset + 1, null);
            table.set(offset + 2, true);
            table.set(offset + 3, false);
        },
    };
    return {
        __proto__: null,
        "./libbitsub_bg.js": import0,
    };
}

const DvbParserFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_dvbparser_free(ptr, 1));
const PgsParserFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_pgsparser_free(ptr, 1));
const RenderResultFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_renderresult_free(ptr, 1));
const SubtitleCompositionFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_subtitlecomposition_free(ptr, 1));
const SubtitleFrameFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_subtitleframe_free(ptr, 1));
const SubtitleRendererFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_subtitlerenderer_free(ptr, 1));
const VobSubFrameFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_vobsubframe_free(ptr, 1));
const VobSubParserFinalization = (typeof FinalizationRegistry === 'undefined')
    ? { register: () => {}, unregister: () => {} }
    : new FinalizationRegistry(ptr => wasm.__wbg_vobsubparser_free(ptr, 1));

function getArrayU8FromWasm0(ptr, len) {
    ptr = ptr >>> 0;
    return getUint8ArrayMemory0().subarray(ptr / 1, ptr / 1 + len);
}

let cachedDataViewMemory0 = null;
function getDataViewMemory0() {
    if (cachedDataViewMemory0 === null || cachedDataViewMemory0.buffer.detached === true || (cachedDataViewMemory0.buffer.detached === undefined && cachedDataViewMemory0.buffer !== wasm.memory.buffer)) {
        cachedDataViewMemory0 = new DataView(wasm.memory.buffer);
    }
    return cachedDataViewMemory0;
}

function getStringFromWasm0(ptr, len) {
    return decodeText(ptr >>> 0, len);
}

let cachedUint8ArrayMemory0 = null;
function getUint8ArrayMemory0() {
    if (cachedUint8ArrayMemory0 === null || cachedUint8ArrayMemory0.byteLength === 0) {
        cachedUint8ArrayMemory0 = new Uint8Array(wasm.memory.buffer);
    }
    return cachedUint8ArrayMemory0;
}

function passArray8ToWasm0(arg, malloc) {
    const ptr = malloc(arg.length * 1, 1) >>> 0;
    getUint8ArrayMemory0().set(arg, ptr / 1);
    WASM_VECTOR_LEN = arg.length;
    return ptr;
}

function passStringToWasm0(arg, malloc, realloc) {
    if (realloc === undefined) {
        const buf = cachedTextEncoder.encode(arg);
        const ptr = malloc(buf.length, 1) >>> 0;
        getUint8ArrayMemory0().subarray(ptr, ptr + buf.length).set(buf);
        WASM_VECTOR_LEN = buf.length;
        return ptr;
    }

    let len = arg.length;
    let ptr = malloc(len, 1) >>> 0;

    const mem = getUint8ArrayMemory0();

    let offset = 0;

    for (; offset < len; offset++) {
        const code = arg.charCodeAt(offset);
        if (code > 0x7F) break;
        mem[ptr + offset] = code;
    }
    if (offset !== len) {
        if (offset !== 0) {
            arg = arg.slice(offset);
        }
        ptr = realloc(ptr, len, len = offset + arg.length * 3, 1) >>> 0;
        const view = getUint8ArrayMemory0().subarray(ptr + offset, ptr + len);
        const ret = cachedTextEncoder.encodeInto(arg, view);

        offset += ret.written;
        ptr = realloc(ptr, len, offset, 1) >>> 0;
    }

    WASM_VECTOR_LEN = offset;
    return ptr;
}

function takeFromExternrefTable0(idx) {
    const value = wasm.__wbindgen_externrefs.get(idx);
    wasm.__externref_table_dealloc(idx);
    return value;
}

let cachedTextDecoder = new TextDecoder('utf-8', { ignoreBOM: true, fatal: true });
cachedTextDecoder.decode();
const MAX_SAFARI_DECODE_BYTES = 2146435072;
let numBytesDecoded = 0;
function decodeText(ptr, len) {
    numBytesDecoded += len;
    if (numBytesDecoded >= MAX_SAFARI_DECODE_BYTES) {
        cachedTextDecoder = new TextDecoder('utf-8', { ignoreBOM: true, fatal: true });
        cachedTextDecoder.decode();
        numBytesDecoded = len;
    }
    return cachedTextDecoder.decode(getUint8ArrayMemory0().subarray(ptr, ptr + len));
}

const cachedTextEncoder = new TextEncoder();

if (!('encodeInto' in cachedTextEncoder)) {
    cachedTextEncoder.encodeInto = function (arg, view) {
        const buf = cachedTextEncoder.encode(arg);
        view.set(buf);
        return {
            read: arg.length,
            written: buf.length
        };
    };
}

let WASM_VECTOR_LEN = 0;

let wasmModule, wasmInstance, wasm;
function __wbg_finalize_init(instance, module) {
    wasmInstance = instance;
    wasm = instance.exports;
    wasmModule = module;
    cachedDataViewMemory0 = null;
    cachedUint8ArrayMemory0 = null;
    wasm.__wbindgen_start();
    return wasm;
}

async function __wbg_load(module, imports) {
    if (typeof Response === 'function' && module instanceof Response) {
        if (typeof WebAssembly.instantiateStreaming === 'function') {
            try {
                return await WebAssembly.instantiateStreaming(module, imports);
            } catch (e) {
                const validResponse = module.ok && expectedResponseType(module.type);

                if (validResponse && module.headers.get('Content-Type') !== 'application/wasm') {
                    console.warn("`WebAssembly.instantiateStreaming` failed because your server does not serve Wasm with `application/wasm` MIME type. Falling back to `WebAssembly.instantiate` which is slower. Original error:\n", e);

                } else { throw e; }
            }
        }

        const bytes = await module.arrayBuffer();
        return await WebAssembly.instantiate(bytes, imports);
    } else {
        const instance = await WebAssembly.instantiate(module, imports);

        if (instance instanceof WebAssembly.Instance) {
            return { instance, module };
        } else {
            return instance;
        }
    }

    function expectedResponseType(type) {
        switch (type) {
            case 'basic': case 'cors': case 'default': return true;
        }
        return false;
    }
}

function initSync(module) {
    if (wasm !== undefined) return wasm;


    if (module !== undefined) {
        if (Object.getPrototypeOf(module) === Object.prototype) {
            ({module} = module)
        } else {
            console.warn('using deprecated parameters for `initSync()`; pass a single object instead')
        }
    }

    const imports = __wbg_get_imports();
    if (!(module instanceof WebAssembly.Module)) {
        module = new WebAssembly.Module(module);
    }
    const instance = new WebAssembly.Instance(module, imports);
    return __wbg_finalize_init(instance, module);
}

async function __wbg_init(module_or_path) {
    if (wasm !== undefined) return wasm;


    if (module_or_path !== undefined) {
        if (Object.getPrototypeOf(module_or_path) === Object.prototype) {
            ({module_or_path} = module_or_path)
        } else {
            console.warn('using deprecated parameters for the initialization function; pass a single object instead')
        }
    }

    if (module_or_path === undefined) {
        module_or_path = new URL('libbitsub_bg.wasm', import.meta.url);
    }
    const imports = __wbg_get_imports();

    if (typeof module_or_path === 'string' || (typeof Request === 'function' && module_or_path instanceof Request) || (typeof URL === 'function' && module_or_path instanceof URL)) {
        module_or_path = fetch(module_or_path);
    }

    const { instance, module } = await __wbg_load(await module_or_path, imports);

    return __wbg_finalize_init(instance, module);
}

export { initSync, __wbg_init as default };
