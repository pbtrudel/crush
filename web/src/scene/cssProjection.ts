import type * as THREE from 'three';

// A port of the camera and object matrix functions in three's
// examples/jsm/renderers/CSS3DRenderer.js (MIT license, Copyright 2010-2025
// three.js authors). The renderer itself would append each object's element
// to its own camera element, which moves the log out of its place in the
// DOM; this applies the same matrices to wrapper elements that stay where
// Preact renders them.
//
// The renderer's `camera.view` branch is left out on purpose: the offset
// belongs to the normal layout, and the scene mount eases it to zero over
// the cinematic flight, so the projected log is placed by the camera
// matrices alone while the offset fades. The camera matrices must be up to
// date when these are called, as they are inside the renderer's render().

function epsilon(value: number): number {
  return Math.abs(value) < 1e-10 ? 0 : value;
}

function cameraMatrix(matrix: THREE.Matrix4): string {
  const e = matrix.elements;
  return (
    'matrix3d(' +
    epsilon(e[0]) + ',' +
    epsilon(-e[1]) + ',' +
    epsilon(e[2]) + ',' +
    epsilon(e[3]) + ',' +
    epsilon(e[4]) + ',' +
    epsilon(-e[5]) + ',' +
    epsilon(e[6]) + ',' +
    epsilon(e[7]) + ',' +
    epsilon(e[8]) + ',' +
    epsilon(-e[9]) + ',' +
    epsilon(e[10]) + ',' +
    epsilon(e[11]) + ',' +
    epsilon(e[12]) + ',' +
    epsilon(-e[13]) + ',' +
    epsilon(e[14]) + ',' +
    epsilon(e[15]) +
    ')'
  );
}

function objectMatrix(matrix: THREE.Matrix4): string {
  const e = matrix.elements;
  return (
    'matrix3d(' +
    epsilon(e[0]) + ',' +
    epsilon(e[1]) + ',' +
    epsilon(e[2]) + ',' +
    epsilon(e[3]) + ',' +
    epsilon(-e[4]) + ',' +
    epsilon(-e[5]) + ',' +
    epsilon(-e[6]) + ',' +
    epsilon(-e[7]) + ',' +
    epsilon(e[8]) + ',' +
    epsilon(e[9]) + ',' +
    epsilon(e[10]) + ',' +
    epsilon(e[11]) + ',' +
    epsilon(e[12]) + ',' +
    epsilon(e[13]) + ',' +
    epsilon(e[14]) + ',' +
    epsilon(e[15]) +
    ')'
  );
}

// The camera element carries the perspective, the camera's inverse world
// matrix and the half-viewport translation that puts the origin at the
// center of the view.
export function cameraCSS(camera: THREE.PerspectiveCamera, width: number, height: number): string {
  const fov = camera.projectionMatrix.elements[5] * (height / 2);
  return (
    `perspective(${fov}px) ` +
    'scale( 1 )' +
    `translateZ(${fov}px)` +
    cameraMatrix(camera.matrixWorldInverse) +
    `translate(${width / 2}px,${height / 2}px)`
  );
}

export function objectCSS(object: THREE.Object3D): string {
  return 'translate(-50%,-50%)' + objectMatrix(object.matrixWorld);
}
