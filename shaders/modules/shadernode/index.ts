/** Defines functions and interfaces for working with a shader.
 *
 * Example:
 *
 *    const node = new ShaderNode(ck, [imageShader1, imageShader2]);
 *    node.compile();
 *    const shader = node.getShader(predefinedUniformValues);
 */
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow';
import { Uniform } from '../../../infra-sk/modules/uniform/uniform';
import {
  CanvasKit,
  MallocObj, RuntimeEffect, Shader,
} from '../../build/canvaskit/canvaskit';
import { ScrapBody, ScrapID } from '../json';

export const predefinedUniforms = `uniform float3 iResolution;      // Viewport resolution (pixels)
uniform float  iTime;            // Shader playback time (s)
uniform float4 iMouse;           // Mouse drag pos=.xy Click pos=.zw (pixels)
uniform float3 iImageResolution; // iImage1 and iImage2 resolution (pixels)
uniform shader iImage1;          // An input image (Mandrill).
uniform shader iImage2;          // An input image (Soccer ball).`;

/** How many of the uniforms listed in predefinedUniforms are of type 'shader'? */
export const numPredefinedShaderUniforms = predefinedUniforms.match(/^uniform shader/gm)!.length;

/**
 * Counts the number of uniforms defined in 'predefinedUniforms'. All the
 * remaining uniforms that start with 'i' will be referred to as "user
 * uniforms".
 */
export const numPredefinedUniforms = predefinedUniforms.match(/^uniform/gm)!.length - numPredefinedShaderUniforms;

/**
 * Counts the number of controls that handle pre-defined uniforms.
 *
 * Takes into account the uniform-fps-sk which doesn't correspond to a uniform.
 */
export const numPredefinedUniformControls = numPredefinedUniforms + 1;

/**
 * The number of lines prefixed to every shader for predefined uniforms. Needed
 * to properly adjust error line numbers.
 */
export const numPredefinedUniformLines = predefinedUniforms.split('\n').length;

/**
 * Regex that finds lines in shader compiler error messages that mention a line number
 * and makes that line number available as a capture.
 */
export const shaderCompilerErrorRegex = /^error: (\d+)/i;

/** The default shader to fall back to if nothing can be loaded. */
export const defaultShader = `half4 main(float2 fragCoord) {
  return vec4(1, 0, 0, 1);
}`;

const defaultBody: ScrapBody = {
  Type: 'sksl',
  Body: defaultShader,
  SKSLMetaData: {
    Uniforms: [],
    Children: [],
  },
};

/**
 * Called ShaderNode because once we support child shaders this will be just one
 * node in a tree of shaders.
 */
export class ShaderNode {
    /** The scrap ID this shader was last saved as. */
    private scrapID: string = '';

    /** The saved configuration of the shader. */
    private body: ScrapBody | null = null;

    /** The shader code compiled. */
    private effect: RuntimeEffect | null = null;

    private inputImageShaders: Shader[] = [];

    private canvasKit: CanvasKit;

    private uniforms: Uniform[] = [];

    private uniformFloatCount: number = 0;

    /**
     * Keep a MallocObj around to pass uniforms to the shader to avoid the need
     * to make copies.
     */
    private uniformsMallocObj: MallocObj | null = null;

    /**
     * Records the code that is currently running, which might differ from the
     * code in the editor, and the code that was last saved.
     */
    private runningCode = defaultShader;

    /**
     * The current code in the editor, which might differ from the currently
     * running code, and the code that was last saved.
     */
    private _shaderCode = defaultShader;

    private _compileErrorMessage: string = '';

    private _compileErrorLineNumbers: number[] = [];

    /**
     * These are the uniform values for all the user defined uniforms. They
     * exclude the predefined uniform values.
     */
    private _currentUserUniformValues: number[] = [];

    private _numPredefinedUniformValues: number = 0;

    constructor(canvasKit: CanvasKit, inputImageShaders: Shader[]) {
      this.canvasKit = canvasKit;
      if (inputImageShaders.length !== numPredefinedShaderUniforms) {
        throw new Error(`ShaderNode requires exactly two predefined image shaders, got ${inputImageShaders.length}`);
      }
      this.inputImageShaders = inputImageShaders;
      this.body = defaultBody;
    }

    /** Loads a scrap from the backend for the given scrap id. */
    async loadScrap(scrapID: string): Promise<void> {
      this.scrapID = scrapID;
      const resp = await fetch(`/_/load/${scrapID}`, {
        credentials: 'include',
      });
      const scrapBody = (await jsonOrThrow(resp)) as ScrapBody;
      this.setScrap(scrapBody);
    }

    /** Sets the code and uniforms of a shader to run. */
    setScrap(scrapBody: ScrapBody): void {
      this.body = scrapBody;
      this._shaderCode = this.body.Body;
      this.currentUserUniformValues = this.body.SKSLMetaData?.Uniforms || [];
      this.compile();
    }

    /**
     * Saves the scrap to the backend returning a Promise that resolves to the
     * scrap id that it was stored at, or reject on an error.
     */
    async saveScrap(): Promise<string> {
      const body: ScrapBody = {
        Body: this._shaderCode,
        Type: 'sksl',
        SKSLMetaData: {
          Uniforms: this._currentUserUniformValues,
          Children: [],
        },
      };

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
      this.scrapID = json.Hash;
      this.body = body;
      return this.scrapID;
    }

    /**
     * The possibly edited shader code. If compile has not been called then it
     * may differ from the current running code as embodied in the getShader()
     * response.
     */
    get shaderCode(): string { return this._shaderCode; }

    set shaderCode(val: string) {
      this._shaderCode = val;
    }

    /**
     * The values that should be used for the used defined uniforms, as opposed
     * to the predefined uniform values.
     * */
    get currentUserUniformValues(): number[] { return this._currentUserUniformValues; }

    set currentUserUniformValues(val: number[]) {
      this._currentUserUniformValues = val;
    }

    /**
     * The full shader compile error message. Only updated on a call to
     * compile().
     */
    get compileErrorMessage(): string {
      return this._compileErrorMessage;
    }

    /** The line numbers that errors occurred on. Only updated on a call to
     * compile(). */
    get compileErrorLineNumbers(): number[] {
      return this._compileErrorLineNumbers;
    }

    /** Compiles the shader code for this node. */
    compile(): void {
      this._compileErrorMessage = '';
      this._compileErrorLineNumbers = [];
      this.runningCode = this._shaderCode;
      // eslint-disable-next-line no-unused-expressions
      this.effect?.delete();
      this.effect = this.canvasKit!.RuntimeEffect.Make(`${predefinedUniforms}\n${this.runningCode}`, (err) => {
      // Fix up the line numbers on the error messages, because they are off by
      // the number of lines we prefixed with the predefined uniforms. The regex
      // captures the line number so we can replace it with the correct value.
      // While doing the fix up of the error message we also annotate the
      // corresponding lines in the CodeMirror editor.
        err = err.replace(shaderCompilerErrorRegex, (_match, firstRegexCaptureValue): string => {
          const lineNumber = (+firstRegexCaptureValue - (numPredefinedUniformLines + 1));
          this._compileErrorLineNumbers.push(lineNumber);
          return `error: ${lineNumber.toFixed(0)}`;
        });
        this._compileErrorMessage = err;
      });

      // Do some precalculations to avoid bouncing into WASM code too much.
      this.uniformFloatCount = this.effect?.getUniformFloatCount() || 0;
      this.buildUniformsFromEffect();
      this.calcNumPredefinedUniformValues();
      this.mallocUniformsMallocObj();

      // Fix up currentUserUniformValues if it's the wrong length.
      const userUniformValuesLength = this.uniformFloatCount - this._numPredefinedUniformValues;
      if (this.currentUserUniformValues.length !== userUniformValuesLength) {
        this.currentUserUniformValues = new Array(userUniformValuesLength).fill(0.5);
      }
    }

    /** Returns true if this node needs to have its code recompiled. */
    needsCompile(): boolean {
      return (this._shaderCode !== this.runningCode);
    }

    /** Returns true if this node or any child node needs to be saved. */
    needsSave(): boolean {
      return (this._shaderCode !== this.body!.Body) || this.userUniformValuesHaveBeenEdited();
    }

    /** Returns the number of uniforms in the effect. */
    getUniformCount(): number {
      return this.uniforms.length;
    }

    /** Get a description of the uniform at the given index. */
    getUniform(index: number): Uniform {
      return this.uniforms[index];
    }

    /** The total number of floats across all predefined and user uniforms. */
    getUniformFloatCount(): number {
      return this.uniformFloatCount;
    }

    /**
     * This is really only called once every raf for the shader that has focus,
     * i.e. that shader that is being displayed on the web UI.
     */
    getShader(predefinedUniformsValues: number[]): Shader | null {
      if (!this.effect) {
        return null;
      }
      const uniformsFloat32Array: Float32Array = this.uniformsMallocObj!.toTypedArray() as Float32Array;

      // Copy in predefined uniforms values.
      predefinedUniformsValues.forEach((val, index) => { uniformsFloat32Array[index] = val; });

      // Copy in our local edited uniform values to the right spots.
      this.currentUserUniformValues.forEach((val, index) => { uniformsFloat32Array[index + this._numPredefinedUniformValues] = val; });

      return this.effect!.makeShaderWithChildren(uniformsFloat32Array, false, this.inputImageShaders);
    }

    get numPredefinedUniformValues(): number {
      return this._numPredefinedUniformValues;
    }

    /** The number of floats that are defined by predefined uniforms. */
    private calcNumPredefinedUniformValues(): void {
      this._numPredefinedUniformValues = 0;
      if (!this.effect) {
        return;
      }
      for (let i = 0; i < numPredefinedUniforms; i++) {
        const u = this.uniforms[i];
        this._numPredefinedUniformValues += u.rows * u.columns;
      }
    }

    /**
     * Builds this._uniforms from this.effect, which is used to avoid later
     * repeated calls into WASM.
     */
    private buildUniformsFromEffect() {
      this.uniforms = [];
      if (!this.effect) {
        return;
      }
      const count = this.effect.getUniformCount();
      for (let i = 0; i < count; i++) {
        // Use object spread operator to clone the SkSLUniform and add a name to make a Uniform.
        this.uniforms.push({ ...this.effect.getUniform(i), name: this.effect.getUniformName(i) });
      }
    }

    private mallocUniformsMallocObj(): void {
      // Copy uniforms into this.uniformsMallocObj, which is kept around to avoid
      // copying overhead in WASM.
      if (this.uniformsMallocObj) {
        this.canvasKit!.Free(this.uniformsMallocObj);
      }
      this.uniformsMallocObj = this.canvasKit!.Malloc(Float32Array, this.uniformFloatCount);
    }

    private userUniformValuesHaveBeenEdited(): boolean {
      const savedLocalUniformValues = this.body?.SKSLMetaData?.Uniforms || [];
      if (this._currentUserUniformValues.length !== savedLocalUniformValues.length) {
        return true;
      }
      for (let i = 0; i < this._currentUserUniformValues.length; i++) {
        if (this._currentUserUniformValues[i] !== savedLocalUniformValues[i]) {
          return true;
        }
      }
      return false;
    }
}
