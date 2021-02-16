/**
 * @module modules/shaders-app-sk
 * @description <h2><code>shaders-app-sk</code></h2>
 *
 */
import { $ } from 'common-sk/modules/dom';
import 'codemirror/mode/clike/clike'; // Syntax highlighting for c-like languages.
import { define } from 'elements-sk/define';
import { html, TemplateResult } from 'lit-html';
import { errorMessage } from 'elements-sk/errorMessage';
import CodeMirror from 'codemirror';
import { $$ } from 'common-sk/modules/dom';
import { stateReflector } from 'common-sk/modules/stateReflector';
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow';
import { HintableObject } from 'common-sk/modules/hintable';
import { isDarkMode } from '../../../infra-sk/modules/theme-chooser-sk/theme-chooser-sk';
import type {
  CanvasKit,
  Surface,
  Canvas,
  RuntimeEffect,
  Paint,
  MallocObj,
  Shader,
} from '../../build/canvaskit/canvaskit.js';

import 'elements-sk/error-toast-sk';
import 'elements-sk/styles/buttons';
import 'elements-sk/styles/select';
import '../../../infra-sk/modules/theme-chooser-sk';
import { SKIA_VERSION } from '../../build/version';
import { ElementSk } from '../../../infra-sk/modules/ElementSk/ElementSk';
import { ScrapBody, ScrapID } from '../json';
import '../../../infra-sk/modules/uniform-time-sk';
import '../../../infra-sk/modules/uniform-generic-sk';
import '../../../infra-sk/modules/uniform-dimensions-sk';
import '../../../infra-sk/modules/uniform-slider-sk';
import '../../../infra-sk/modules/uniform-mouse-sk';
import '../../../infra-sk/modules/uniform-color-sk';
import '../../../infra-sk/modules/uniform-imageresolution-sk';
import { Uniform, UniformControl } from '../../../infra-sk/modules/uniform/uniform';
import { FPS } from '../fps/fps';
import { DimensionsChangedEventDetail } from '../../../infra-sk/modules/uniform-dimensions-sk/uniform-dimensions-sk';

// eslint-disable-next-line @typescript-eslint/no-var-requires
const CanvasKitInit = require('../../build/canvaskit/canvaskit.js');

// This element might be loaded from a different site, and that means we need
// to be careful about how we construct the URL back to the canvas.wasm file.
// Start by recording the script origin.
const scriptOrigin = new URL((document!.currentScript as HTMLScriptElement).src)
  .origin;
const kitReady = CanvasKitInit({
  locateFile: (file: any) => `${scriptOrigin}/dist/${file}`,
});

const DEFAULT_SIZE = 512;

const predefinedUniforms = `uniform float3 iResolution;      // Viewport resolution (pixels)
uniform float  iTime;            // Shader playback time (s)
uniform float4 iMouse;           // Mouse drag pos=.xy Click pos=.zw (pixels)
uniform float3 iImageResolution; // iImage1 and iImage2 resolution (pixels)
uniform shader iImage1;          // An input image (Mandrill).
uniform shader iImage2;          // An input image (Soccer ball).`;

// How many of the uniforms listed in predefinedUniforms are of type 'shader'?
const numPredefinedShaderUniforms = predefinedUniforms.match(/^uniform shader/gm)!.length;

// Counts the number of uniforms defined in 'predefinedUniforms'. All the
// remaining uniforms that start with 'i' will be referred to as "user
// uniforms".
const numPredefinedUniforms = predefinedUniforms.match(/^uniform/gm)!.length - numPredefinedShaderUniforms;

// The number of lines prefixed to every shader for predefined uniforms. Needed
// to properly adjust error line numbers.
const numPredefinedUniformLines = predefinedUniforms.split('\n').length;

const defaultShader = `half4 main(float2 fragCoord) {
  return vec4(1, 0, 0, 1);
}`;

// Regex that finds lines in shader compiler error messages that mention a line number
// and makes that line number available as a capture.
const shaderCompilerErrorRegex = /^error: (\d+)/i;

type stateChangedCallback = ()=> void;

// State represents data reflected to/from the URL.
interface State {
  id: string;
}

const defaultState: State = {
  id: '@default',
};

// CodeMirror likes mode definitions as maps to bools, but a string of space
// separated words is easier to edit, so we convert between the two format.
function words(str: string): {[key: string]: boolean} {
  const obj: any = {};
  str.split(/\s+/).forEach((word) => {
    if (!word) {
      return
    }
    obj[word] = true;
  });
  return obj;
}

// See the design doc for the list of keywords. http://go/shaders.skia.org.
const keywords = `const attribute uniform varying break continue
  discard return for while do if else struct in out inout uniform layout`;
const blockKeywords = 'case do else for if switch while struct enum union';
const defKeywords = 'struct enum union';
const builtins = `radians degrees
  sin cos tan asin acos atan
  pow exp log exp2 log2
  sqrt inversesqrt
  abs sign floor ceil fract mod
  min max clamp saturate
  mix step smoothstep
  length distance dot cross normalize
  faceforward reflect refract
  matrixCompMult inverse
  lessThan lessThanEqual greaterThan greaterThanEqual equal notEqual
  any all not
  sample unpremul `;

const types = `int long char short double float unsigned
  signed void bool float float2 float3 float4
  float2x2 float3x3 float4x4
  half half2 half3 half4
  half2x2 half3x3 half4x4
  bool bool2 bool3 bool4
  int int2 int3 int4
  fragmentProcessor shader
  vec2 vec3 vec4
  ivec2 ivec3 ivec4
  bvec2 bvec3 bvec4
  mat2 mat3 mat4`;

// Define a new mode and mime-type for SkSL shaders. We follow the shader naming
// covention found in CodeMirror.
CodeMirror.defineMIME('x-shader/x-sksl', {
  name: 'clike',
  keywords: words(keywords),
  types: words(types),
  builtin: words(builtins),
  blockKeywords: words(blockKeywords),
  defKeywords: words(defKeywords),
  typeFirstDefinitions: true,
  atoms: words('sk_FragCoord true false'),
  modeProps: { fold: ['brace', 'include'] },
});


// requestAnimationFrame id if requestAnimationFrame is not running.
const RAF_NOT_RUNNING = -1;

export class ShadersAppSk extends ElementSk {
  private width: number = 512;

  private height: number = 512;

  private codeMirror: CodeMirror.Editor | null = null;

  private canvasEle: HTMLCanvasElement | null = null;

  private kit: CanvasKit | null = null;

  private canvasKitContext: number = -1;

  private surface: Surface | null = null;

  private canvas: Canvas | null = null;

  private paint: Paint | null = null;

  private inputImageShaders: Shader[] = [];

  private effect: RuntimeEffect | null = null;

  private state: State = defaultState;

  // If not the empty string, this contains the full last shader compiler error
  // message.
  private compileErrorMessage: string = '';

  // Records the lines that have been marked as having errors. We keep these
  // around so we can clear the error annotations efficiently.
  private compileErrorLines: CodeMirror.TextMarker[] = [];

  // Keep a MallocObj around to pass uniforms to the shader to avoid the need to
  // make copies.
  private uniformsMallocObj: MallocObj | null = null;

  // The requestAnimationFrame id if we are running, otherwise we are not running.
  private rafID: number = RAF_NOT_RUNNING;

  // Records the code that we started with, either at startup, or after we've saved.
  private lastSavedCode = defaultShader;

  // Records the code that is currently running.
  private runningCode = defaultShader;

  // The current code in the editor.
  private editedCode = defaultShader;

  // These are the uniform values for all the user defined uniforms. They
  // exclude the predefined uniform values.
  private lastSavedUserUniformValues: number[] = [];

  // These are the uniform values for all the user defined uniforms. They
  // exclude the predefined uniform values.
  private currentUserUniformValues: number[] = [];

  // stateReflector update function.
  private stateChanged: stateChangedCallback | null = null;

  private fps: FPS = new FPS();

  constructor() {
    super(ShadersAppSk.template);
  }

  private static uniformControls = (ele: ShadersAppSk): TemplateResult[] => {
    const ret: TemplateResult[] = [];
    const effect = ele.effect;
    if (!effect) {
      return ret;
    }
    for (let i = 0; i < effect.getUniformCount(); i++) {
      // Use object spread operator to clone the SkSLUniform and add a name to make a Uniform.
      const uniform: Uniform = { ...effect.getUniform(i), name: effect.getUniformName(i) };
      if (!uniform.name.startsWith('i')) {
        continue;
      }
      switch (uniform.name) {
        case 'iTime':
          ret.push(html`<uniform-time-sk .uniform=${uniform}></uniform-time-sk>`);
          break;
        case 'iMouse':
          ret.push(html`<uniform-mouse-sk .uniform=${uniform} .elementToMonitor=${ele.canvasEle}></uniform-mouse-sk>`);
          break;
        case 'iResolution':
          ret.push(html`
            <uniform-dimensions-sk
              .uniform=${uniform}
              @dimensions-changed=${ele.dimensionsChanged}
            ></uniform-dimensions-sk>`);
          break;
        case 'iImageResolution':
          ret.push(html`<uniform-imageresolution-sk .uniform=${uniform}></uniform-imageresolution-sk>`);
          break;
        default:
          if (uniform.name.toLowerCase().indexOf('color') !== -1) {
            ret.push(html`<uniform-color-sk .uniform=${uniform}></uniform-color-sk>`);
          } else if (uniform.rows === 1 && uniform.columns === 1) {
            ret.push(html`<uniform-slider-sk .uniform=${uniform}></uniform-slider-sk>`);
          } else {
            ret.push(html`<uniform-generic-sk .uniform=${uniform}></uniform-generic-sk>`);
          }
          break;
      }
    }
    return ret;
  }

  private static template = (ele: ShadersAppSk) => html`
    <header>
      <h2><a href="/">SkSL Shaders</a></h2>
      <span>
        <a
          id="githash"
          href="https://skia.googlesource.com/skia/+show/${SKIA_VERSION}"
        >
          ${SKIA_VERSION.slice(0, 7)}
        </a>
        <theme-chooser-sk dark></theme-chooser-sk>
      </span>
    </header>
    <main>
      <div>
        <p @click=${ele.fastLoad}>Examples: <a href="/?id=@inputs">Uniforms</a> <a href="/?id=@iResolution">iResolution</a> <a href="/?id=@iTime">iTime</a> <a href="/?id=@iMouse">iMouse</a> <a href="/?id=@iImage">iImage</a></p>
        <canvas
          id="player"
          width=${ele.width}
          height=${ele.height}
        >
          Your browser does not support the canvas tag.
        </canvas>
      </div>
      <div>
        <details id=shaderinputs>
          <summary>Shader Inputs</summary>
          <textarea rows=${numPredefinedUniformLines} cols=75 readonly id="predefinedShaderInputs">${predefinedUniforms}</textarea>
          <div id=imageSources>
            <figure>
              <img id=iImage1 loading="eager" src="/dist/mandrill.png">
              <figcaption>iImage1</figcaption>
            </figure>
            <figure>
              <img id=iImage2 loading="eager" src="/dist/soccer.png">
              <figcaption>iImage2</figcaption>
            </figure>
        </div>
        </details>
        <div id="codeEditor"></div>
        <div ?hidden=${!ele.compileErrorMessage} id="compileErrors">
          <h3>Errors</h3>
          <pre>${ele.compileErrorMessage}</pre>
        </div>
      </div>
      <div id=shaderControls>
        <div id=fps>
          ${ele.fps.fps.toFixed(0)} fps
        </div>
        <div id=uniformControls>
          ${ShadersAppSk.uniformControls(ele)}
        </div>
        <button
          ?hidden=${ele.editedCode === ele.runningCode}
          @click=${ele.runClick}
          class=action
        >
          Run
        </button>
        <button
          ?hidden=${ele.editedCode === ele.lastSavedCode && !ele.userUniformValuesHaveBeenEdited()}
          @click=${ele.saveClick}
          class=action
        >
          Save
        </button>
      </div>
    </main>
    <footer>
      <error-toast-sk></error-toast-sk>
    </footer>
  `;

  /** Returns the CodeMirror theme based on the state of the page's darkmode.
   *
   * For this to work the associated CSS themes must be loaded. See
   * shaders-app-sk.scss.
   */
  private static themeFromCurrentMode = () => (isDarkMode() ? 'ambiance' : 'base16-light');

  connectedCallback(): void {
    super.connectedCallback();
    this._render();
    this.canvasEle = $$<HTMLCanvasElement>('#player', this);
    this.codeMirror = CodeMirror($$<HTMLDivElement>('#codeEditor', this)!, {
      lineNumbers: true,
      mode: 'x-shader/x-sksl',
      theme: ShadersAppSk.themeFromCurrentMode(),
      viewportMargin: Infinity,
    });
    this.codeMirror.on('change', () => this.codeChange());

    // Listen for theme changes.
    document.addEventListener('theme-chooser-toggle', () => {
      this.codeMirror!.setOption('theme', ShadersAppSk.themeFromCurrentMode());
    });

    // Continue the setup once CanvasKit WASM has loaded.
    kitReady.then(async (ck: CanvasKit) => {
      this.kit = ck;

      try {
        this.inputImageShaders = [];
        // Wait until all the images are loaded.
        // Note: All shader images MUST be 512 x 512 to agree with iImageResolution.
        const elements = await Promise.all<HTMLImageElement>([this.promiseOnImageLoaded('#iImage1'), this.promiseOnImageLoaded('#iImage2')]);
        // Convert them into shaders.
        elements.forEach((ele) => {
          const image = this.kit!.MakeImageFromCanvasImageSource(ele);
          const shader = image.makeShaderOptions(this.kit!.TileMode.Clamp, this.kit!.TileMode.Clamp, this.kit!.FilterMode.Linear, this.kit!.MipmapMode.None);
          this.inputImageShaders.push(shader);
        });
      } catch (error) {
        errorMessage(error);
      }

      this.paint = new this.kit.Paint();
      try {
        this.stateChanged = stateReflector(
          /* getState */ () => (this.state as unknown) as HintableObject,
          /* setState */ (newState: HintableObject) => {
            this.state = (newState as unknown) as State;
            if (!this.state.id) {
              this.startShader(defaultShader);
            } else {
              this.loadShaderIfNecessary();
            }
          },
        );
      } catch (error) {
        errorMessage(error, 0);
      }
    });
  }

  /**
   * Returns a Promise that resolves when in image loads in an <img> element
   * with the given id.
   */
  private promiseOnImageLoaded(id: string): Promise<HTMLImageElement> {
    return new Promise<HTMLImageElement>((resolve, reject) => {
      const ele = $$<HTMLImageElement>(id, this)!;
      if (ele.complete) {
        resolve(ele);
      } else {
        ele.addEventListener('load', () => resolve(ele));
        ele.addEventListener('error', (e) => reject(e));
      }
    });
  }

  private dimensionsChanged(e: Event) {
    const newDims = (e as CustomEvent<DimensionsChangedEventDetail>).detail;
    this.width = newDims.width;
    this.height = newDims.height;
    this.startShader(this.runningCode);
  }

  private monitorIfDevicePixelRatioChanges() {
    // Use matchMedia to detect if the screen resolution changes from the current value.
    // See https://developer.mozilla.org/en-US/docs/Web/API/Window/devicePixelRatio#monitoring_screen_resolution_or_zoom_level_changes
    const mqString = `(resolution: ${window.devicePixelRatio}dppx)`;
    matchMedia(mqString).addEventListener('change', () => this.startShader(this.runningCode));
  }

  private async loadShaderIfNecessary() {
    if (!this.state.id) {
      return;
    }
    try {
      const resp = await fetch(`/_/load/${this.state.id}`, {
        credentials: 'include',
      });
      const json = (await jsonOrThrow(resp)) as ScrapBody;
      this.lastSavedCode = json.Body;
      this.startShader(json.Body);
      if (json.SKSLMetaData && json.SKSLMetaData.Uniforms !== null) {
        this.setCurrentUserUniformValues(json.SKSLMetaData.Uniforms);
        // We round trip the uniforms through the controls so we are sure to get an exact match.
        this.lastSavedUserUniformValues = this.getCurrentUserUniformValues(this.getUniformValuesFromControls());
      }
    } catch (error) {
      errorMessage(error, 0);
      // Return to the default view.
      this.state = Object.assign({}, defaultState);
      this.stateChanged!();
    }
  }

  private startShader(shaderCode: string) {
    this.monitorIfDevicePixelRatioChanges();
    // Cancel any pending drawFrames.
    if (this.rafID !== RAF_NOT_RUNNING) {
      cancelAnimationFrame(this.rafID);
      this.rafID = RAF_NOT_RUNNING;
    }

    this.runningCode = shaderCode;
    this.editedCode = shaderCode;
    this.codeMirror!.setValue(shaderCode);

    // eslint-disable-next-line no-unused-expressions
    this.surface?.delete();
    this.surface = this.kit!.MakeCanvasSurface(this.canvasEle!);
    if (!this.surface) {
      errorMessage('Could not make Surface.', 0);
      return;
    }
    // We don't need to call .delete() on the canvas because
    // the parent surface will do that for us.
    this.canvas = this.surface.getCanvas();
    this.canvasKitContext = this.kit!.currentContext();
    // eslint-disable-next-line no-unused-expressions
    this.effect?.delete();
    this.clearAllEditorErrorAnnotations();
    this.compileErrorMessage = '';
    this.effect = this.kit!.RuntimeEffect.Make(`${predefinedUniforms}\n${shaderCode}`, (err) => {
      // Fix up the line numbers on the error messages, because they are off by
      // the number of lines we prefixed with the predefined uniforms. The regex
      // captures the line number so we can replace it with the correct value.
      // While doing the fix up of the error message we also annotate the
      // corresponding lines in the CodeMirror editor.
      err = err.replace(shaderCompilerErrorRegex, (_match, firstRegexCaptureValue): string => {
        const lineNumber = (+firstRegexCaptureValue - (numPredefinedUniformLines + 1));
        this.setEditorErrorLineAnnotation(lineNumber);
        return `error: ${lineNumber.toFixed(0)}`;
      });
      this.compileErrorMessage = err;
    });
    // Render so the uniform controls get displayed.
    this._render();

    if (!this.effect) {
      return;
    }

    this.drawFrame();
  }

  private clearAllEditorErrorAnnotations(): void{
    // eslint-disable-next-line no-unused-expressions
    this.compileErrorLines?.forEach((textMarker) => {
      textMarker.clear();
    });
  }

  private setEditorErrorLineAnnotation(lineNumber: number): void {
    // Set the class of that line to 'cm-error'.
    this.compileErrorLines.push(this.codeMirror!.markText(
      { line: lineNumber - 1, ch: 0 },
      { line: lineNumber - 1, ch: 200 }, // Some large number for the character offset.
      {
        className: 'cm-error', // See the base16-dark.css file in CodeMirror for the class name.
      },
    ));
  }

  private getUniformValuesFromControls(): number[] {
    // Populate the uniforms values from the controls.
    const uniforms: number[] = new Array(this.effect!.getUniformFloatCount());
    $('#uniformControls > *').forEach((control) => {
      (control as unknown as UniformControl).applyUniformValues(uniforms);
    });
    return uniforms;
  }

  private setUniformValuesToControls(uniforms: number[]): void {
    // Populate the control values from the uniforms.
    $('#uniformControls > *').forEach((control) => {
      (control as unknown as UniformControl).restoreUniformValues(uniforms);
    });
  }

  private userUniformValuesHaveBeenEdited(): boolean {
    if (this.currentUserUniformValues.length !== this.lastSavedUserUniformValues.length) {
      return true;
    }
    for (let i = 0; i < this.currentUserUniformValues.length; i++) {
      if (this.currentUserUniformValues[i] !== this.lastSavedUserUniformValues[i]) {
        return true;
      }
    }
    return false;
  }

  private totalPredefinedUniformValues(): number {
    let ret = 0;
    if (!this.effect) {
      return 0;
    }
    for (let i = 0; i < numPredefinedUniforms; i++) {
      const u = this.effect.getUniform(i);
      ret += u.rows * u.columns;
    }
    return ret;
  }

  private setCurrentUserUniformValues(userUniformValues: number[]): void {
    if (this.effect) {
      const uniforms = this.getUniformValuesFromControls();
      // Update only the non-predefined uniform values.
      const begin = this.totalPredefinedUniformValues();
      for (let i = begin; i < this.effect.getUniformFloatCount(); i++) {
        uniforms[i] = userUniformValues[i - begin];
      }
      this.setUniformValuesToControls(uniforms);
    }
  }

  private getCurrentUserUniformValues(uniforms: number[]): number[] {
    const uniformsArray: number[] = [];
    if (this.effect) {
      // Return only the non-predefined uniform values.
      for (let i = this.totalPredefinedUniformValues(); i < this.effect.getUniformFloatCount(); i++) {
        uniformsArray.push(uniforms[i]);
      }
    }
    return uniformsArray;
  }

  private drawFrame() {
    this.fps.raf();
    this.kit!.setCurrentContext(this.canvasKitContext);
    const uniformsArray = this.getUniformValuesFromControls();
    this.currentUserUniformValues = this.getCurrentUserUniformValues(uniformsArray);

    // Copy uniforms into this.uniformsMallocObj, which is kept around to avoid
    // copying overhead in WASM.
    if (!this.uniformsMallocObj) {
      this.uniformsMallocObj = this.kit!.Malloc(Float32Array, uniformsArray.length);
    } else if (this.uniformsMallocObj.length !== uniformsArray.length) {
      this.kit!.Free(this.uniformsMallocObj);
      this.uniformsMallocObj = this.kit!.Malloc(Float32Array, uniformsArray.length);
    }
    const uniformsFloat32Array: Float32Array = this.uniformsMallocObj.toTypedArray() as Float32Array;
    uniformsArray.forEach((val, index) => { uniformsFloat32Array[index] = val; });

    const shader = this.effect!.makeShaderWithChildren(uniformsFloat32Array, true, this.inputImageShaders);
    this._render();

    // Allow uniform controls to update, such as uniform-timer-sk.
    this._render();

    // Draw the shader.
    this.canvas!.clear(this.kit!.BLACK);
    this.paint!.setShader(shader);
    const rect = this.kit!.XYWHRect(0, 0, this.width, this.height);
    this.canvas!.drawRect(rect, this.paint!);
    this.surface!.flush();

    this.rafID = requestAnimationFrame(() => {
      this.drawFrame();
    });
  }

  private async runClick() {
    this.startShader(this.editedCode);
    this.saveClick();
  }

  private async saveClick() {
    const userUniformValues = this.getCurrentUserUniformValues(this.getUniformValuesFromControls());
    const body: ScrapBody = {
      Body: this.editedCode,
      Type: 'sksl',
      SKSLMetaData: {
        Uniforms: userUniformValues,
        Children: [],
      },
    };
    try {
      // POST the JSON to /_/upload
      const resp = await fetch('/_/save/', {
        credentials: 'include',
        body: JSON.stringify(body),
        headers: {
          'Content-Type': 'application/json',
        },
        method: 'POST',
      });
      const json = (await jsonOrThrow(resp)) as ScrapID;

      this.state.id = json.Hash;
      this.lastSavedCode = this.editedCode;
      this.lastSavedUserUniformValues = userUniformValues;
      this.stateChanged!();
      this._render();
    } catch (error) {
      errorMessage(`${error}`, 0);
    }
  }

  private codeChange() {
    this.editedCode = this.codeMirror!.getValue();
    this._render();
  }

  /**
   * Load example by changing state rather than actually following the links.
   */
  private fastLoad(e: Event): void{
    const ele = (e.target as HTMLLinkElement);
    if (ele.tagName !== 'A') {
      return;
    }
    e.preventDefault();
    const id = new URL(ele.href).searchParams.get('id') || '';
    this.state.id = id;
    this.stateChanged!();
    this.loadShaderIfNecessary();
  }
}

define('shaders-app-sk', ShadersAppSk);
